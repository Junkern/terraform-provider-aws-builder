# AGENTS.md — aws-provider-build

## Purpose

This repository builds a custom, reduced Terraform AWS provider from a pinned
upstream `hashicorp/terraform-provider-aws` revision. It is not the provider
itself and must remain independent of the provider module.

## Repository shape

- `main.go`: standalone Go CLI; standard library only.
- `README.md`: user, compatibility, and release documentation.
- `AGENTS.md`: contributor instructions and design constraints.

Do not add imports from `github.com/hashicorp/terraform-provider-aws` to this
repository. The tool must download provider source at build time and apply all
changes only inside a temporary checkout.

## Required workflow

Before changing behavior, read `README.md` and inspect the exact pinned upstream
revision. Run:

```sh
go fmt ./...
go test ./...
go build -o aws-provider-build .
./aws-provider-build --source-version v6.64.0 --services s3 --output ./dist
```

The final command is an end-to-end test, not just a compilation test. It must
load Terraform schema and prove `aws_s3_bucket` and an S3 data source are
available while `aws_instance` is unavailable. Do not require AWS credentials
for these checks. If Terraform is unavailable, fail clearly; do not silently
skip schema validation.

When testing without network access, set `AWS_PROVIDER_SOURCE_URL` to a local
Git mirror containing the requested tag/commit. Always verify the recorded
commit in the output metadata.

## Overlay rules

The current supported service selection is exactly `s3`. Keep these boundaries
explicit:

- Registered service packages: `s3` only.
- Linked AWS client factories: `s3`, `s3control`, and `sts`.
- S3 Control is needed for S3 transparent tag operations; STS supports standard
  account/partition discovery.
- S3 list resources remain because they are emitted by the upstream S3 service
  package. Actions and ephemeral selection is not independently configurable.
- Provider endpoint schemas are currently inherited unchanged from upstream.
  Do not claim that the public schema is S3-only until versioned overlays for
  both generated provider schema files exist.

Generated registry files should be rendered as complete deterministic outputs.
Do not maintain copied provider generated files or mutate the user's checkout.
The `main.go` address transformation must remain anchored and fail when its
expected source shape is absent or ambiguous. Prefer explicit, version-aware
transformations over broad regular expressions.

## Reproducibility and supply chain

- Default to the official upstream repository.
- Resolve tags/commits to a full commit ID and record it.
- Preserve exact Go build flags, target platform, provider address, and SHA-256
  in metadata.
- Do not add dependencies without a clear need; the CLI intentionally uses the
  Go standard library and external `git`, `go`, and `terraform` commands.
- Treat source URL mirrors as trusted supply-chain inputs and document them in
  build records.
- Never run arbitrary user-provided commands from configuration.

## Compatibility, licensing, and releases

The output provider address is `local/aws-slim`, not
`registry.terraform.io/hashicorp/aws`. Document new lock-file/checksum behavior
and state migration implications for every release. A custom binary needs an
independent rebuild and review for every upstream security update. Preserve the
upstream Terraform AWS Provider MPL-2.0 license and notices when redistributing
provider source or binaries.

Do not claim AWS operation support from schema tests alone. Keep real S3 smoke
tests optional, isolated, and credential-gated. Never commit credentials,
state files, downloaded provider trees, or build output.
