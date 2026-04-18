# s3lo

[![CI](https://github.com/OuFinx/s3lo/actions/workflows/ci.yml/badge.svg)](https://github.com/OuFinx/s3lo/actions/workflows/ci.yml)
[![Release](https://github.com/OuFinx/s3lo/actions/workflows/release.yml/badge.svg)](https://github.com/OuFinx/s3lo/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/OuFinx/s3lo)](https://goreportcard.com/report/github.com/OuFinx/s3lo)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Use S3, GCS, Azure Blob, or local storage as a container image registry. Faster pulls, cheaper storage, no registry to manage.

## Why s3lo?

| | ECR | s3lo |
|---|---|---|
| **Pull speed** | ~1-5 Gbps | Up to 100 Gbps on EC2 |
| **Storage cost** | $0.10/GB/month | $0.023/GB/month (S3 Standard) |
| **Registry management** | Lifecycle policies, permissions | Just a bucket |
| **Multi-region** | Replicate per region | Native cloud replication |
| **Cloud support** | AWS only | AWS S3, GCS, Azure Blob, MinIO, R2, Ceph |

## Quick Start

### Install

**Quick install (recommended):**
```bash
curl -sSL https://raw.githubusercontent.com/OuFinx/s3lo/main/install.sh | sh
```

**Homebrew (macOS/Linux):**
```bash
brew install OuFinx/tap/s3lo
```

**Manual download:**

<details>
<summary>Platform-specific binaries</summary>

**macOS (Apple Silicon):**
```bash
curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_darwin_arm64.tar.gz
tar xzf s3lo.tar.gz && sudo mv s3lo /usr/local/bin/
```

**macOS (Intel):**
```bash
curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_darwin_amd64.tar.gz
tar xzf s3lo.tar.gz && sudo mv s3lo /usr/local/bin/
```

**Linux (amd64):**
```bash
curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
tar xzf s3lo.tar.gz && sudo mv s3lo /usr/local/bin/
```

**Linux (arm64):**
```bash
curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_arm64.tar.gz
tar xzf s3lo.tar.gz && sudo mv s3lo /usr/local/bin/
```

**From source:**
```bash
go install github.com/OuFinx/s3lo/cmd/s3lo@latest
```

</details>

### Usage

```bash
# Push a local Docker image to S3
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

# Pull from S3 into local Docker
s3lo pull s3://my-bucket/myapp:v1.0

# Copy from any registry — bare names work just like docker pull
s3lo copy alpine:latest s3://my-bucket/alpine:latest
s3lo copy nginx:1.25 s3://my-bucket/nginx:1.25
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
s3lo copy s3://source-bucket/myapp:v1.0 s3://dest-bucket/myapp:v1.0

# List images in a bucket
s3lo list s3://my-bucket/

# Inspect image metadata
s3lo inspect s3://my-bucket/myapp:v1.0

# Show storage stats and deduplication savings
s3lo stats s3://my-bucket/

# Delete a tag
s3lo delete s3://my-bucket/myapp:v1.0

# Configure lifecycle rules (keep last 10 tags, max 90 days)
s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d

# Clean old tags and unreferenced blobs (dry run by default)
s3lo clean s3://my-bucket/
s3lo clean s3://my-bucket/ --confirm

# Analyze bucket configuration and get recommendations
s3lo config recommend s3://my-bucket/

# Enable per-image tag immutability
s3lo config set s3://my-bucket/myapp immutable=true

# Show push history
s3lo history s3://my-bucket/
s3lo history s3://my-bucket/myapp

# Browse and manage images interactively
s3lo tui s3://my-bucket/

# Sign an image with AWS KMS (FIPS 140-2, CloudTrail audit log)
s3lo sign s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer

# Sign with a local key file
COSIGN_PASSWORD=secret s3lo sign s3://my-bucket/myapp:v1.0 --key cosign.key

# Verify a signature (exit 0 = valid, 1 = invalid/missing, 2 = infra error)
s3lo verify s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer
s3lo verify s3://my-bucket/myapp:v1.0 --key cosign.pub --output json

# --- Google Cloud Storage ---
s3lo push myapp:v1.0 gs://my-gcs-bucket/myapp:v1.0
s3lo pull gs://my-gcs-bucket/myapp:v1.0
s3lo list gs://my-gcs-bucket/

# --- Azure Blob Storage ---
AZURE_STORAGE_ACCOUNT=mystorageaccount s3lo push myapp:v1.0 az://my-container/myapp:v1.0
AZURE_STORAGE_ACCOUNT=mystorageaccount s3lo pull az://my-container/myapp:v1.0

# --- S3-compatible (MinIO, Cloudflare R2, Ceph) ---
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --endpoint http://localhost:9000
s3lo pull s3://my-bucket/myapp:v1.0 --endpoint http://localhost:9000

# --- Local storage (no cloud account needed) ---

# Initialize local storage
s3lo init --local ./local-s3

# Push and pull with local://
s3lo push myapp:v1.0 local://./local-s3/myapp:v1.0
s3lo pull local://./local-s3/myapp:v1.0
s3lo list local://./local-s3/
s3lo history local://./local-s3/
```

## How It Works

s3lo stores container images on S3 using the [OCI Image Layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) format. Each layer is stored as a separate S3 object, enabling parallel downloads and cross-image deduplication.

```
s3://my-bucket/myapp/v1.0/
--�------ index.json              # OCI Image Index
--�------ manifest.json           # OCI Manifest
--�------ config.json             # Image Config
--------- blobs/sha256/
    --�------ a1b2c3d4...         # Layer 1 (shared with other images)
    --�------ e5f6g7h8...         # Layer 2
    --------- i9j0k1l2...         # Layer 3
```

**Push** exports a Docker image, splits it into content-addressable layers, and uploads them to S3 in parallel. Existing layers are skipped (deduplication via SHA256).

**Pull** downloads layers from S3 in parallel and imports the image into the local Docker daemon.

## Authentication

s3lo uses the standard credential chain for each cloud:

- **AWS S3**: standard AWS credentials chain — environment variables, `~/.aws/credentials`, IAM instance profiles, SSO, etc.
- **GCS**: Application Default Credentials — `GOOGLE_APPLICATION_CREDENTIALS`, `gcloud auth application-default login`, or attached service account.
- **Azure Blob**: `DefaultAzureCredential` — service principal env vars, `az login`, or managed identity. Set `AZURE_STORAGE_ACCOUNT` to your storage account name.
- **S3-compatible**: same as AWS — set `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` for the target service, and pass `--endpoint`.

### AWS S3 — Minimum IAM Policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:HeadObject",
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": [
        "arn:aws:s3:::YOUR-BUCKET",
        "arn:aws:s3:::YOUR-BUCKET/*"
      ]
    }
  ]
}
```

For read-only access (pull only), remove `s3:PutObject`.

## CI Integration

### GitHub Actions

```yaml
- name: Push image to S3
  run: |
    curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
    tar xzf s3lo.tar.gz && chmod +x s3lo
    ./s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:${{ github.sha }}
```

### GitLab CI

```yaml
push-image:
  script:
    - curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
    - tar xzf s3lo.tar.gz && chmod +x s3lo
    - ./s3lo push myapp:${CI_COMMIT_SHA} s3://my-bucket/myapp:${CI_COMMIT_SHA}
```

## Use as a Go Library

s3lo exposes its core packages for use in other tools:

```go
import (
    "github.com/OuFinx/s3lo/pkg/ref"     // Parse s3://, gs://, az://, local:// references
    "github.com/OuFinx/s3lo/pkg/oci"     // OCI manifest parsing, Docker export/import
    "github.com/OuFinx/s3lo/pkg/storage" // Storage client (S3, GCS, Azure Blob, local)
    "github.com/OuFinx/s3lo/pkg/image"   // High-level push/pull/list/inspect
)
```

All public APIs accept `context.Context` for cancellation and timeout support.

## Documentation

See the [documentation site](https://oufinx.github.io/s3lo/) for the full reference: all commands with detailed flags and examples, S3 storage layout, deduplication mechanics, IAM policies, Go library usage, CI integration patterns, and FAQ.

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[MIT](LICENSE)
