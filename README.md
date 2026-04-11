# s3lo

[![CI](https://github.com/OuFinx/s3lo/actions/workflows/ci.yml/badge.svg)](https://github.com/OuFinx/s3lo/actions/workflows/ci.yml)
[![Release](https://github.com/OuFinx/s3lo/actions/workflows/release.yml/badge.svg)](https://github.com/OuFinx/s3lo/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/OuFinx/s3lo)](https://goreportcard.com/report/github.com/OuFinx/s3lo)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Use AWS S3 as a container image registry. Faster pulls, cheaper storage, no registry to manage.

## Why s3lo?

| | ECR | s3lo + S3 |
|---|---|---|
| **Pull speed** | ~1-5 Gbps | Up to 100 Gbps on EC2 |
| **Storage cost** | $0.10/GB/month | $0.023/GB/month (S3 Standard) |
| **Registry management** | Lifecycle policies, permissions | Just an S3 bucket |
| **Multi-region** | Replicate per region | S3 Cross-Region Replication |

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

# Copy from ECR or another S3 bucket (no local Docker needed)
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
s3lo copy s3://source-bucket/myapp:v1.0 s3://dest-bucket/myapp:v1.0

# List images in a bucket
s3lo list s3://my-bucket/

# Inspect image metadata
s3lo inspect s3://my-bucket/myapp:v1.0

# Show storage stats and deduplication savings
s3lo stats s3://my-bucket/

# Delete a tag (blobs remain; run gc to reclaim)
s3lo delete s3://my-bucket/myapp:v1.0

# Garbage collect unreferenced blobs
s3lo gc s3://my-bucket/             # dry run
s3lo gc s3://my-bucket/ --confirm   # delete

# Apply declarative retention policies
s3lo lifecycle apply s3://my-bucket/ --config lifecycle.yaml
s3lo lifecycle apply s3://my-bucket/ --config lifecycle.yaml --confirm

# Generate S3 Lifecycle Rule Terraform for the bucket
s3lo recommend s3://my-bucket/

# Enable tag immutability
s3lo config set s3://my-bucket/ immutable=true

# First-time interactive setup
s3lo configure
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

s3lo uses the standard AWS credentials chain --- environment variables, `~/.aws/credentials`, IAM instance profiles, SSO, etc.

### Minimum IAM Policy

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
    "github.com/OuFinx/s3lo/pkg/ref"   // Parse s3://bucket/image:tag references
    "github.com/OuFinx/s3lo/pkg/oci"   // OCI manifest parsing, Docker export/import
    "github.com/OuFinx/s3lo/pkg/s3"    // S3 client with region auto-detection
    "github.com/OuFinx/s3lo/pkg/image" // High-level push/pull/list/inspect
)
```

All public APIs accept `context.Context` for cancellation and timeout support.

## Documentation

See [GUIDE.md](GUIDE.md) for the full feature reference: all commands with detailed flags and examples, S3 storage layout, deduplication mechanics, IAM policies, Go library usage, CI integration patterns, and FAQ.

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[MIT](LICENSE)
