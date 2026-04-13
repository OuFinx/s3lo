# serve

Start an HTTP server that speaks the [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec/blob/main/spec.md), serving images stored in the given bucket.

Enables `docker pull`, `kubectl`, and any OCI client to pull images directly from S3 — without running `s3lo pull` first.

```
s3lo serve <s3-ref> [flags]
```

## Arguments

| Argument | Description |
|----------|-------------|
| `<s3-ref>` | Bucket reference: `s3://bucket/`, `gs://bucket/`, `az://container/`, `local://path/` |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `5000` | Port to listen on |
| `--host` | `127.0.0.1` | Bind address |
| `--tls-cert` | | TLS certificate file (enables HTTPS) |
| `--tls-key` | | TLS key file |
| `--presign-ttl` | `1h` | TTL for S3 presigned blob URLs |

## What it does

Implements the following OCI Distribution Spec endpoints:

| Method | Path | Action |
|--------|------|--------|
| `GET` | `/v2/` | OCI version check (200 OK) |
| `HEAD/GET` | `/v2/<name>/manifests/<ref>` | Fetch manifest by tag or digest |
| `HEAD/GET` | `/v2/<name>/blobs/<digest>` | Serve blob (presigned redirect or stream) |

**Manifest lookup:** `<ref>` can be a tag (e.g. `v1.0`) or a digest (e.g. `sha256:abc123...`). The `Docker-Content-Digest` header is always set so clients can verify integrity.

**Blob serving strategy:**

- **S3 / S3-compatible:** Blobs are served via 303 redirect to a presigned GET URL. No blob data passes through the server.
- **GCS, Azure, local:** Blobs are streamed from the backend through the server.

## Examples

```bash
# Serve from S3, listen on localhost:5000
s3lo serve s3://my-bucket/ --port 5000

# Pull from it with Docker
docker pull localhost:5000/myapp:v1.0

# Expose on all interfaces (e.g. for remote nodes)
s3lo serve s3://my-bucket/ --host 0.0.0.0 --port 5000

# Enable HTTPS with a TLS certificate
s3lo serve s3://my-bucket/ --host 0.0.0.0 --tls-cert cert.pem --tls-key key.pem

# MinIO / S3-compatible endpoint
s3lo serve s3://my-bucket/ --endpoint http://minio:9000

# GCS bucket
s3lo serve gs://my-gcs-bucket/
```

## Output

```
Serving s3://my-bucket/ at http://127.0.0.1:5000
Blob strategy: presigned URLs (S3)
Press Ctrl+C to stop.
```

For non-S3 backends:

```
Serving gs://my-gcs-bucket/ at http://127.0.0.1:5000
Blob strategy: streaming (GCS)
Press Ctrl+C to stop.
```

## Notes

- The server does not implement authentication. For production use, place it behind a reverse proxy (nginx, Caddy, etc.) or restrict access with `--host` and firewall rules.
- For large images on GCS, Azure, or local backends, `s3lo pull` is more efficient — the streaming path loads the entire blob into memory before forwarding.
- The `--presign-ttl` flag controls how long presigned S3 URLs remain valid. Increase it if clients are slow to start downloading after receiving the redirect.
