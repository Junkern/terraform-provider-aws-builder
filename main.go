// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: MPL-2.0

// Command aws-provider-build builds a pinned, reduced Terraform AWS provider.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const upstreamURL = "https://github.com/hashicorp/terraform-provider-aws.git"

func main() {
	var sourceVersion, services, output string
	flag.StringVar(&sourceVersion, "source-version", "v6.64.0", "upstream tag or commit")
	flag.StringVar(&services, "services", "s3", "comma-separated service package names")
	flag.StringVar(&output, "output", "./dist", "output directory")
	flag.Parse()
	selected := splitServices(services)
	if len(selected) == 0 {
		fatal("--services must contain at least one service")
	}
	if err := build(sourceVersion, selected, output); err != nil {
		fatal(err.Error())
	}
}

func build(sourceVersion string, services []string, output string) error {
	ctx := context.Background()
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "aws-provider-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	source := filepath.Join(tmp, "source")
	sourceURL := os.Getenv("AWS_PROVIDER_SOURCE_URL")
	if sourceURL == "" {
		sourceURL = upstreamURL
	}
	if _, err := run(ctx, "git", "clone", "--filter=blob:none", "--no-checkout", sourceURL, source); err != nil {
		return fmt.Errorf("cloning upstream source: %w", err)
	}
	if _, err := run(ctx, "git", "-C", source, "checkout", "--detach", sourceVersion); err != nil {
		return fmt.Errorf("checking out source version: %w", err)
	}
	commit, err := run(ctx, "git", "-C", source, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	commit = strings.TrimSpace(commit)
	if err := writeOverlays(source, services); err != nil {
		return err
	}
	binary := filepath.Join(output, "terraform-provider-aws-slim")
	start := time.Now()
	ldflags := "-s -w -X main.providerAddress=local/aws-slim -X github.com/hashicorp/terraform-provider-aws/version.ProviderVersion=" + sourceVersion
	if _, err := runDir(ctx, source, "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", binary, "."); err != nil {
		return fmt.Errorf("building provider: %w", err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	meta := map[string]any{"source_commit": commit, "source_version": sourceVersion, "goos": runtime.GOOS, "goarch": runtime.GOARCH, "size_bytes": len(data), "sha256": hex.EncodeToString(hash[:]), "build_seconds": time.Since(start).Seconds()}
	encoded, _ := json.MarshalIndent(meta, "", "  ")
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(output, "terraform-provider-aws-slim.build.json"), encoded, 0o644); err != nil {
		return err
	}
	return nil
}

func writeOverlays(source string, services []string) error {
	packages, err := generateServicePackages(source, services)
	if err != nil {
		return err
	}
	clients, err := generateAWSClients(source, services)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "internal/provider/sdkv2/service_packages_gen.go"), packages, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "internal/conns/awsclient_gen.go"), clients, 0o644); err != nil {
		return err
	}
	path := filepath.Join(source, "main.go")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	old := "func main() {}"
	if bytes.Count(body, []byte("func main() {")) != 1 {
		return errors.New("main.go shape changed: expected one func main")
	}
	body = bytes.Replace(body, []byte("func main() {"), []byte("var providerAddress = \"registry.terraform.io/hashicorp/aws\"\n\nfunc main() {"), 1)
	old = "\"registry.terraform.io/hashicorp/aws\","
	if bytes.Count(body, []byte(old)) != 1 {
		return errors.New("main.go shape changed: expected one provider address")
	}
	body = bytes.Replace(body, []byte(old), []byte("providerAddress,"), 1)
	return os.WriteFile(path, body, 0o644)
}

func splitServices(value string) []string {
	var out []string
	for _, s := range strings.Split(value, ",") {
		s = strings.TrimSpace(s)
		if s != "" && !slicesContains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
func slicesContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func generateServicePackages(source string, selected []string) ([]byte, error) {
	body, err := os.ReadFile(filepath.Join(source, "internal/provider/sdkv2/service_packages_gen.go"))
	if err != nil {
		return nil, err
	}
	allowed := append(append([]string{}, selected...), "account", "partition", "region", "sts")
	imports := regexp.MustCompile(`(?m)^\s*"github.com/hashicorp/terraform-provider-aws/internal/service/([^"]+)"$`)
	calls := regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)\.ServicePackage\(ctx\),$`)
	var im, cs []string
	for _, m := range imports.FindAllStringSubmatch(string(body), -1) {
		if slicesContains(allowed, m[1]) {
			im = append(im, m[0])
		}
	}
	for _, m := range calls.FindAllStringSubmatch(string(body), -1) {
		if slicesContains(allowed, m[1]) {
			cs = append(cs, "\t\t"+m[1]+".ServicePackage(ctx),")
		}
	}
	if len(cs) == 0 {
		return nil, fmt.Errorf("no service packages found for %s", strings.Join(selected, ","))
	}
	var b strings.Builder
	b.WriteString("// Copyright IBM Corp. 2014, 2026\n// SPDX-License-Identifier: MPL-2.0\n\n// Code generated by aws-provider-build; DO NOT EDIT.\npackage sdkv2\n\nimport (\n\t\"context\"\n\t\"slices\"\n\n")
	for _, i := range im {
		b.WriteString(i + "\n")
	}
	b.WriteString("\t\"github.com/hashicorp/terraform-provider-aws/internal/conns\"\n)\n\nfunc servicePackages(ctx context.Context) []conns.ServicePackage {\n\tv := []conns.ServicePackage{\n")
	for _, c := range cs {
		b.WriteString(c + "\n")
	}
	b.WriteString("\t}\n\treturn slices.Clone(v)\n}\n")
	return []byte(b.String()), nil
}

func generateAWSClients(source string, selected []string) ([]byte, error) {
	path := filepath.Join(source, "internal/conns/awsclient_gen.go")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(body)
	// Include client accessors actually referenced by selected service implementations.
	method := regexp.MustCompile(`\.([A-Za-z0-9]+Client)\(`)
	needed := map[string]bool{}
	for _, svc := range append(append([]string{}, selected...), "account", "partition", "region", "sts") {
		root := filepath.Join(source, "internal/service", svc)
		filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
			if e == nil && info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
				if d, e := os.ReadFile(path); e == nil {
					for _, m := range method.FindAllStringSubmatch(string(d), -1) {
						needed[m[1]] = true
					}
				}
			}
			return nil
		})
	}
	for _, n := range []string{"STSClient", "S3ControlClient", "ResourceGroupsTaggingAPIClient"} {
		needed[n] = true
	}
	funcs := regexp.MustCompile(`(?ms)^func \(c \*AWSClient\) ([A-Za-z0-9]+Client)\(.*?\n}\n`)
	var fs []string
	for _, m := range funcs.FindAllStringSubmatch(text, -1) {
		if needed[m[1]] {
			fs = append(fs, m[0])
		}
	}
	usedSDK := map[string]bool{}
	for _, f := range fs {
		if m := regexp.MustCompile(`\*([a-z0-9]+)\.Client`).FindStringSubmatch(f); len(m) == 2 {
			usedSDK[m[1]] = true
		}
	}
	imports := regexp.MustCompile(`(?m)^\s*"github.com/aws/aws-sdk-go-v2/service/([^\"]+)"$`)
	var im []string
	for _, m := range imports.FindAllStringSubmatch(text, -1) {
		if usedSDK[m[1]] {
			im = append(im, m[0])
		}
	}
	if len(fs) == 0 {
		return nil, errors.New("no AWS client accessors found")
	}
	var b strings.Builder
	b.WriteString("// Copyright IBM Corp. 2014, 2026\n// SPDX-License-Identifier: MPL-2.0\n\n// Code generated by aws-provider-build; DO NOT EDIT.\npackage conns\n\nimport (\n\t\"context\"\n\n")
	for _, i := range im {
		b.WriteString(i + "\n")
	}
	b.WriteString("\t\"github.com/hashicorp/terraform-provider-aws/internal/errs\"\n\t\"github.com/hashicorp/terraform-provider-aws/names\"\n)\n\n")
	for _, f := range fs {
		b.WriteString(f + "\n")
	}
	return []byte(b.String()), nil
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	return runEnv(ctx, os.Environ(), name, args...)
}
func runDir(ctx context.Context, dir, name string, args ...string) (string, error) {
	return runEnvDir(ctx, dir, os.Environ(), name, args...)
}
func runEnv(ctx context.Context, env []string, name string, args ...string) (string, error) {
	return runEnvDir(ctx, "", env, name, args...)
}
func runEnvDir(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
