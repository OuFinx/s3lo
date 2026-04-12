# copy

Copy an image to S3 from any OCI registry or another S3 bucket — without going through the local Docker daemon.

```
s3lo copy <src> <s3-dest> [flags]
```

## Arguments

| Argument | Description |
|----------|-------------|
| `<src>` | Source image. S3/local reference or OCI registry reference (see formats below). |
| `<s3-dest>` | Destination in `s3://bucket/image:tag` or `local://path/image:tag` format. Tag is required for both S3/local source and destination. |

## Flags

| Flag | Description |
|------|-------------|
| `--platform <os/arch>` | Copy only the specified platform, e.g. `linux/amd64`. Default: copy all platforms. |

## Source formats

| Format | Example |
|--------|---------|
| Bare name (Docker Hub official) | `alpine`, `nginx:1.25` |
| User image (Docker Hub) | `user/myapp:v1.0` |
| Full registry | `ghcr.io/owner/image:tag` |
| ECR | `123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0` |
| S3 | `s3://source-bucket/myapp:v1.0` |
| Local | `local://./local-s3/myapp:v1.0` |

Bare names resolve exactly like `docker pull` — `alpine` becomes `docker.io/library/alpine:latest`.

## Examples

```bash
# Mirror from Docker Hub (bare name, like docker pull)
s3lo copy alpine:latest s3://my-bucket/alpine:latest
s3lo copy nginx:1.25 s3://my-bucket/nginx:1.25

# Mirror a specific platform only
s3lo copy alpine:latest s3://my-bucket/alpine:latest --platform linux/amd64

# Copy from ECR (auto-authenticates with your AWS credentials)
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0

# Copy from GitHub Container Registry
s3lo copy ghcr.io/owner/myapp:v1.0 s3://my-bucket/myapp:v1.0

# Promote between S3 buckets (server-side copy within same bucket — free and instant)
s3lo copy s3://staging-bucket/myapp:v1.0 s3://prod-bucket/myapp:v1.0

# Copy from local storage to S3
s3lo copy local://./local-s3/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

## Multi-arch behavior

By default, `copy` copies all platforms from a multi-arch image and preserves the OCI Image Index at the destination. Users pulling from the destination will get the full multi-arch image.

Use `--platform` to copy only one platform (results in a single-arch image at the destination):

```bash
# Copy only linux/amd64 from a multi-arch alpine
s3lo copy alpine:latest s3://my-bucket/alpine:latest --platform linux/amd64
```

## How it works

=== "Registry → S3"

    1. Fetches all platform manifests from the registry in parallel (using the OCI Distribution API).
    2. Deduplicates blobs across platforms — shared layers are uploaded only once.
    3. Uploads all unique blobs to S3 in parallel (up to 10 concurrent workers).
    4. Writes the manifest to S3.

    For ECR: calls `ecr:GetAuthorizationToken` using your AWS credentials.
    For Docker Hub and others: uses the standard Bearer token challenge flow.

=== "S3 → S3 (same bucket)"

    Uses S3 server-side `CopyObject` — no data transfer, no egress cost, near-instant.

=== "S3 → S3 (different buckets)"

    Streams each blob from the source bucket to the destination. Blobs already present at the destination are skipped.

## Output

```
Copying alpine:latest to s3://my-bucket/alpine:latest
  copying ⠸ 47.3 MB [1m05s]
Done. 7 platform(s) copied, 23 blob(s) copied, 0 skipped.
```
