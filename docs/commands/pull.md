# pull

Download an image from S3 and import it into local Docker.

```
s3lo pull <s3-ref> [image-tag] [flags]
```

## Arguments

| Argument | Description |
|----------|-------------|
| `<s3-ref>` | Source in `s3://bucket/image:tag` or `local://path/image:tag` format. Tag is required. |
| `[image-tag]` | Optional tag to apply after import. Defaults to `image:tag` from the ref. |

## Flags

| Flag | Description |
|------|-------------|
| `--platform <os/arch>` | Select a specific platform from a multi-arch image, e.g. `linux/amd64`. Default: auto-detect host platform. |

## What it does

1. Downloads `manifest.json` from S3.
2. If the image is a multi-arch OCI Image Index, selects the platform matching the host (or `--platform`).
3. Downloads all blobs (config + layers) in parallel.
4. Reconstructs an OCI Image Layout on disk.
5. Imports it into the local Docker daemon via `docker load`.
6. Optionally applies the `[image-tag]` label.

## Examples

```bash
# Pull and import (platform auto-detected for multi-arch images)
s3lo pull s3://my-bucket/myapp:v1.0

# Pull with a custom local tag
s3lo pull s3://my-bucket/myapp:v1.0 myapp:local

# Pull a specific platform from a multi-arch image
s3lo pull s3://my-bucket/alpine:latest --platform linux/amd64

# Pull and run immediately
s3lo pull s3://my-bucket/myapp:v1.0 && docker run --rm myapp:v1.0

# Pull from local storage
s3lo pull local://./local-s3/alpine:latest
```

## Output

```
Pulling s3://my-bucket/myapp:v1.0
  downloading ⠸ 58.7 MB [30s]
Done. Image imported into Docker.
```

## Platform auto-detection

On macOS, `darwin/arm64` is automatically normalized to `linux/arm64` — container images are always Linux. You don't need to pass `--platform` in most cases.

| Host | Auto-detected platform |
|------|----------------------|
| Linux amd64 | `linux/amd64` |
| Linux arm64 | `linux/arm64` |
| macOS Apple Silicon | `linux/arm64` |
| macOS Intel | `linux/amd64` |

!!! note
    If the image is single-arch (not an OCI Image Index), `--platform` is ignored and the single manifest is always used.

!!! warning "Explicit tag required"
    The tag must be specified explicitly — `s3lo pull s3://my-bucket/myapp` (without `:tag`) is an error. This prevents accidental pulls that silently default to `:latest`.
