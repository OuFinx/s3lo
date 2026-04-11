# Multi-Architecture Images

s3lo supports multi-architecture images natively since v1.3.0, using the OCI Image Index format.

## What is a multi-arch image?

A multi-arch image is a single tag that points to multiple platform-specific images. When you run `docker pull alpine:latest`, Docker automatically picks the right platform for your machine — the same tag works on `linux/amd64`, `linux/arm64`, `linux/arm/v7`, and more.

Under the hood, a multi-arch tag is an **OCI Image Index** — a JSON document that lists platform-specific manifests by digest:

```json
{
  "mediaType": "application/vnd.oci.image.index.v1+json",
  "manifests": [
    {
      "digest": "sha256:a1b2...",
      "platform": { "os": "linux", "architecture": "amd64" }
    },
    {
      "digest": "sha256:b2c3...",
      "platform": { "os": "linux", "architecture": "arm64" }
    }
  ]
}
```

s3lo stores this index as `manifest.json` and stores each platform manifest as a blob.

## copy

By default, `copy` copies all platforms and preserves the full OCI Image Index at the destination:

```bash
s3lo copy alpine:latest s3://my-bucket/alpine:latest
# Copies all 7 platforms, preserves multi-arch index
```

To copy only one platform:

```bash
s3lo copy alpine:latest s3://my-bucket/alpine:latest --platform linux/amd64
# Stores a single-arch image at the destination
```

## pull

`pull` automatically selects the right platform for the host machine:

| Host | Auto-selected platform |
|------|----------------------|
| Linux amd64 | `linux/amd64` |
| Linux arm64 | `linux/arm64` |
| macOS Apple Silicon | `linux/arm64` |
| macOS Intel | `linux/amd64` |

macOS normalizes to `linux/*` because container images are always Linux.

Override with `--platform`:

```bash
s3lo pull s3://my-bucket/myapp:v1.0 --platform linux/arm64
```

## push

`push` exports whatever platform is in the local Docker daemon and stores it as a single-arch image. Docker on Apple Silicon produces `linux/arm64` images by default.

To push a specific platform:

```bash
# Build for a specific platform first
docker build --platform linux/amd64 -t myapp:v1.0 .
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

To create a true multi-arch image in S3, use `copy` from a registry that already has the multi-arch tag.

## inspect

`inspect` shows per-platform details for multi-arch images:

```bash
s3lo inspect s3://my-bucket/alpine:latest

# Reference: s3://my-bucket/alpine:latest
# Type:      multi-arch image index (7 platform(s))
#
#   Platform: linux/amd64   Layers: 4  Size: 7.80 MB
#   Platform: linux/arm64   Layers: 4  Size: 7.20 MB
#   Platform: linux/arm/v7  Layers: 4  Size: 6.90 MB
#   ...
```
