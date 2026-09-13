# AWS Provider build

`aws-provider-build` builds a reduced Terraform AWS provider from an immutable
upstream Git revision. A version with only S3 service enabled is "just" 56 Megabytes.


## Quick start

Simplest way is to checkout the [AWS provider](https://github.com/hashicorp/terraform-provider-aws) and remove any AWS service you don't need from the following to files:
* `internal/provider/sdkv2/service_packages_gen.go`
* `internal/conns/awsclient_gen.go`

and compile with `go build -trimpath -buildvcs=false -ldflags "-s -w ...`

For a more controlled way, you can use the `main.go` script offered. Build the tool itself, then run it from any directory:

```sh
go build -o aws-provider-build .
./aws-provider-build --source-version v6.64.0 --services s3,ec2,dynamodb --output ./dist
```
The default source is `https://github.com/hashicorp/terraform-provider-aws.git`.
`--source-version` accepts a tag or a reachable commit and defaults to
`v6.64.0`; it is checked out and resolved to a full commit ID before any source
files are written. `--services` currently accepts only `s3`. `--output` defaults
to `./dist`.

For an air-gapped or mirrored build, set `AWS_PROVIDER_SOURCE_URL` to a Git URL
that contains the requested revision:

```sh
AWS_PROVIDER_SOURCE_URL=https://git.example/aws-provider.git \
  ./aws-provider-build --source-version v6.64.0 --services s3 --output ./dist
```

The tool requires Git and Go 1.26 or newer (matching the v6.64.0 provider module). AWS credentials are not needed.
The build may download Go modules required by the pinned provider.

## Terraform installation

The binary identifies as `local/aws-slim`. For local use, create a CLI config:

```hcl
provider_installation {
  dev_overrides {
    "local/aws-slim" = "/absolute/path/to/dist"
  }
  direct {}
}
```

Declare `source = "local/aws-slim"` in `required_providers`. Development
overrides load the provider directly and intentionally bypass normal registry
installation and lock-file selection. Do not run `terraform init` expecting a
registry version to be selected for this local address.

For distribution, place the binary in a Terraform filesystem mirror using the
`local/aws-slim` hostname/namespace, platform directory, and chosen version,
then create a new `.terraform.lock.hcl`. Checksums for
`registry.terraform.io/hashicorp/aws` are not valid for this provider address.
The tool currently does not publish a mirror or generate lock files itself.

## What the tool does

The intended standalone repository consists of this directory's `main.go`,
`README.md`, and `AGENTS.md`. It has no imports from the Terraform AWS provider
module. The provider source is downloaded at build time and is never modified in
its original checkout.

1. Creates a temporary blobless Git clone and explicitly checks out the requested
   revision in detached HEAD state.
2. Replaces `internal/provider/sdkv2/service_packages_gen.go` with a generated
   registry containing only `internal/service/s3`.
3. Replaces `internal/conns/awsclient_gen.go` with generated client methods for
   S3, S3 Control, and STS. S3 Control is required by S3 transparent-tagging
   helpers; STS supports normal provider account/partition discovery.
4. Applies an anchored transformation to the temporary `main.go`: it introduces
   a provider-address variable and changes the `tf5server.Serve` argument to
   that variable. The transformation fails if the expected upstream shape does
   not occur exactly once.
5. Builds with `-trimpath -buildvcs=false -ldflags "-s -w ..."` and writes
   `terraform-provider-aws-slim` plus JSON metadata containing source commit,
   target version, platform, size, build duration, and SHA-256.
6. Loads the resulting provider through Terraform and checks that
   `aws_s3_bucket` and an S3 data source exist while `aws_instance` produces
   Terraform's `Invalid resource type` diagnostic.
7. Removes the temporary clone.

The generated overlay files are complete replacements, not edits to copied
upstream files. This is intentional: they are generated registry outputs and
keeps the tool independent of internal generator implementation details. The
per-service `internal/service/s3/service_package_gen.go` remains the upstream
file, so all ordinary S3 resources and data sources in that revision are kept.
S3 list resources that are part of that service package remain enabled; list,
action, and ephemeral filtering is not yet exposed as an independent switch.


## Compatibility and safety

This is a custom provider distribution, not an upstream release. Its address is
intentionally different, so state using `registry.terraform.io/hashicorp/aws`
will not be selected automatically. A provider-address migration may require
`terraform state replace-provider`; back up state and rehearse the migration.
Non-S3 resources and data sources are unavailable and their state cannot be
managed by this binary.

Every build should record and retain the metadata JSON and source commit. Rebuild
for every upstream provider release, AWS SDK update, security advisory, or local
patch. Review upstream changes to provider configuration, service registration,
client generation, protocol behavior, and Terraform compatibility before
upgrading the pinned revision. Preserve the upstream MPL-2.0 license and
notices in any distribution.

The tool does not contact AWS. A real S3 smoke test remains optional and must be
run separately with deliberately scoped credentials and an isolated test
account/bucket.

## Known boundary and remaining work

The linked binary still contains shared Terraform SDK/Framework, protocol/mux,
AWS base configuration, Smithy/AWS runtime, tagging, partition/region, STS, and
S3 Control code. Go linker elimination removes most unregistered service
implementation code, but this is not a minimal AWS runtime.

Future work should add versioned schema overlays, a cache for verified source
clones, detached-signature/checksum verification for source provenance, a
filesystem-mirror publisher, optional list/action/ephemeral selection, and
cross-platform acceptance tests.
