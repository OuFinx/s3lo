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
| `--port` | `5000` | Port to listen on (`0` picks a free port and prints it) |
| `--host` | `127.0.0.1` | Bind address. Anything other than loopback requires `--allow-anonymous` |
| `--allow-anonymous` | `false` | Confirms that unauthenticated access is intended. Required for a non-loopback `--host` |
| `--tls-cert` | | TLS certificate file (enables HTTPS; requires `--tls-key`) |
| `--tls-key` | | TLS key file (requires `--tls-cert`) |
| `--verify-key` | | Verification key (`.pub`, `awskms://`, `hashivault://`): serve only images signed by it |
| `--presign-ttl` | `15m` | TTL for S3 presigned blob URLs (max `168h`, the SigV4 limit) |
| `--cache-entries` | `1000` | Manifests to keep cached in memory (`0` = unlimited) |
| `--cache-ttl` | `5m` | How long a cached manifest stays valid |
| `--max-concurrent` | `64` | Maximum concurrent object-storage operations (`0` = unlimited) |

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

# Expose on all interfaces (e.g. for remote nodes).
# There is no authentication, so this needs an explicit opt-in AND TLS,
# and should still be fenced off with a firewall or security group.
s3lo serve s3://my-bucket/ --host 0.0.0.0 --port 5000 --allow-anonymous \
  --tls-cert cert.pem --tls-key key.pem

# Refuse to serve any image that is not signed by this key
s3lo serve s3://my-bucket/ --verify-key cosign.pub

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

- The server does not implement authentication: anyone who can reach the port can read every image in the bucket, including the presigned URLs it hands out for blobs. It binds `127.0.0.1` by default, and a non-loopback `--host` is refused unless `--allow-anonymous` is passed. For production use, put it behind an authenticating reverse proxy (nginx, Caddy, etc.), serve it over TLS, and restrict access with firewall or security-group rules.
- `--verify-key` makes the server refuse any manifest without a valid signature from that key, which is what `s3lo sign` exists for. A pull by bare digest is only served once the same content has been verified through its tag in this process.
- Every request is written to the access log (method, path, status, bytes, client address). Query strings and headers are never logged, so presigned URL signatures do not end up on disk.
- For large images on GCS, Azure, or local backends, `s3lo pull` is more efficient — the streaming path loads the entire blob into memory before forwarding.
- The `--presign-ttl` flag controls how long presigned S3 URLs remain valid. Increase it if clients are slow to start downloading after receiving the redirect. Values above `168h` are rejected: SigV4 will not sign for longer, and S3 would refuse the resulting URLs.
- Repository names and tags are validated against the OCI grammar before dispatch. A malformed name or reference is answered with `400 NAME_INVALID` / `TAG_INVALID` / `DIGEST_INVALID` rather than being passed through to storage.
