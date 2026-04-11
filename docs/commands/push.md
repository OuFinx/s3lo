# push

Export a local Docker image and upload it to S3.

```
s3lo push <local-image> <s3-ref> [flags]
```

## Arguments

| Argument | Description |
|----------|-------------|
| `<local-image>` | Image name and tag as shown in `docker images`, e.g. `myapp:v1.0` |
| `<s3-ref>` | Destination in `s3://bucket/image:tag` format |

## Flags

| Flag | Description |
|------|-------------|
| `--force` | Overwrite an existing tag even if immutability is enabled |

## What it does

1. Exports the image from the local Docker daemon as a tar archive.
2. Parses the OCI Image Layout from the archive.
3. Checks which blobs already exist in `blobs/sha256/` on S3.
4. Uploads missing blobs to `blobs/sha256/<digest>` using S3 Intelligent-Tiering.
5. Uploads manifest files to `manifests/<image>/<tag>/`.

Only missing blobs are uploaded — blobs shared with other images in the bucket are skipped automatically.

## Examples

```bash
# Push a tagged image
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

# Push with a git commit SHA
s3lo push myapp:$(git rev-parse --short HEAD) s3://my-bucket/myapp:$(git rev-parse --short HEAD)

# Push to a nested image name
s3lo push backend:latest s3://my-bucket/org/backend:latest

# Force-overwrite an immutable tag
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --force
```

## Output

```
Pushing myapp:v1.0 to s3://my-bucket/myapp:v1.0
  uploading ⠸ 58.7 MB [45s]
Done.
```

The progress bar shows bytes uploaded and elapsed time. It's hidden automatically in non-TTY environments (CI).

## Notes

!!! info "Image must exist locally"
    The image must be available in your local Docker daemon (`docker images`). If not, run `docker pull myapp:v1.0` first.

!!! tip "Apple Silicon and EKS"
    On Apple Silicon Macs, Docker images are `linux/arm64` by default. For EKS nodes running `linux/amd64`, build or pull the correct platform explicitly:
    ```bash
    docker build --platform linux/amd64 -t myapp:v1.0 .
    # or
    docker pull --platform linux/amd64 myapp:v1.0
    ```
    Use [`s3lo copy`](copy.md) to mirror multi-arch images directly to S3 without this concern.

!!! tip "Idempotent"
    Pushing the same image twice is safe and fast. The second push skips all blobs and only re-uploads the manifest (a few bytes).
