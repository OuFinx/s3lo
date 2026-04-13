# s3lo serve — Design Spec

**Goal:** Add `s3lo serve` — a lightweight OCI Distribution Spec HTTP server that makes S3-stored images pullable via `docker pull` or any OCI client, without running `s3lo pull` first.

**Architecture:** A new `pkg/serve/` package implements a standard `http.Handler` that translates OCI Distribution Spec requests into storage backend operations. The CLI command in `cmd/s3lo/serve.go` wires flags and starts the HTTP server. No data flows through the server for S3 blobs (presigned URL redirect); GCS, Azure, and local backends stream blob bytes.

**Tech Stack:** Go standard library `net/http`, `pkg/storage` (Backend + new Presigner interface), `pkg/ref`, `github.com/spf13/cobra`

---

## Package Layout

```
pkg/serve/
  server.go    — Server struct, ServeHTTP, route dispatch
  handler.go   — OCI endpoint handlers (version, manifests, blobs)
  presign.go   — Presigner interface + S3 implementation

cmd/s3lo/
  serve.go     — cobra command, flag wiring, ListenAndServe
```

## Server Struct

```go
type Server struct {
    Client     storage.Backend
    Bucket     string
    PresignTTL time.Duration
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

`Server` implements `http.Handler`. Route dispatch is done by parsing the path — no external router dependency.

## OCI Endpoints

| Method    | Path                               | Action                                                   |
|-----------|------------------------------------|----------------------------------------------------------|
| GET       | `/v2/`                             | 200 OK — OCI version check                               |
| HEAD/GET  | `/v2/<name>/manifests/<ref>`       | Fetch `manifests/<name>/<ref>/manifest.json` from storage |
| HEAD/GET  | `/v2/<name>/blobs/<digest>`        | Presigned redirect (S3) or stream (GCS/Azure/local)      |
| Any       | anything else                      | 404 OCI error JSON                                       |

**Manifest by tag:** `<ref>` is a tag → fetch `manifests/<name>/<ref>/manifest.json`.

**Manifest by digest:** `<ref>` starts with `sha256:` → list all tags under `manifests/<name>/`, fetch each `manifest.json`, return the first whose sha256 matches. Linear scan is acceptable (images have few tags).

**HEAD vs GET:** HEAD returns headers only (Content-Type, Content-Length, Docker-Content-Digest). GET returns the body too.

**Docker-Content-Digest header:** Always set on manifest responses — Docker clients use it to verify integrity.

## Blob Serving Strategy

OCI blob digest format: `sha256:<hex>` → storage key: `blobs/sha256/<hex>`.

**S3/S3-compatible:** The S3 `*Client` implements a `Presigner` interface. The server detects this at startup and uses presigned GET URLs (303 redirect). No blob bytes flow through the server.

**GCS, Azure, local:** These backends do not implement `Presigner`. The server falls back to `GetObject` and streams the response body. Simple and correct.

```go
// pkg/serve/presign.go — interface definition
type Presigner interface {
    PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// pkg/storage/s3.go — S3 Client implements Presigner
// func (c *Client) PresignGetObject(ctx, bucket, key, ttl) (string, error)
```

## Error Responses

All errors follow the OCI Distribution Spec error format:

```json
{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}
```

| Situation         | HTTP Status | OCI Code            |
|-------------------|-------------|---------------------|
| Image/tag missing | 404         | `MANIFEST_UNKNOWN`  |
| Blob missing      | 404         | `BLOB_UNKNOWN`       |
| Storage error     | 500         | `INTERNAL_ERROR`    |
| Bad path          | 404         | `UNSUPPORTED`        |

## CLI

```
s3lo serve <s3-ref> [flags]

  --port         int      Port to listen on (default 5000)
  --host         string   Bind address (default "127.0.0.1")
  --tls-cert     string   TLS certificate file (enables HTTPS)
  --tls-key      string   TLS key file
  --presign-ttl  duration TTL for S3 presigned blob URLs (default 1h)
  --endpoint     string   S3-compatible endpoint (MinIO, R2, Ceph, etc.)
```

`<s3-ref>` is a bucket reference: `s3://my-bucket/`, `gs://my-bucket/`, `az://my-container/`, `local://./store/`.

**Example usage:**

```bash
# Start server (S3 — presigned redirects)
s3lo serve s3://my-bucket/ --port 5000

# Pull from it with Docker
docker pull localhost:5000/myapp:v1.0

# Expose on all interfaces with TLS
s3lo serve s3://my-bucket/ --host 0.0.0.0 --port 443 --tls-cert cert.pem --tls-key key.pem

# MinIO / S3-compatible
s3lo serve s3://my-bucket/ --endpoint http://minio:9000
```

**Startup output:**

```
Serving s3://my-bucket/ at http://127.0.0.1:5000
Blob strategy: presigned URLs (S3)
Press Ctrl+C to stop.
```

For non-S3 backends:

```
Serving gs://my-bucket/ at http://127.0.0.1:5000
Blob strategy: streaming (GCS)
Press Ctrl+C to stop.
```

## Content-Type Headers

Manifests must have the correct OCI/Docker media type for clients to parse them. The server reads the `mediaType` field from the manifest JSON and sets it as `Content-Type`. If absent, defaults to `application/vnd.oci.image.manifest.v1+json`.

## Testing

Unit tests in `pkg/serve/` use a fake `storage.Backend` (struct implementing the interface) and `httptest.NewServer`. No real AWS/GCS/Azure required.

Tests cover:
- `GET /v2/` → 200
- `GET /v2/myapp/manifests/latest` → correct manifest body + headers
- `HEAD /v2/myapp/manifests/latest` → headers only, no body
- `GET /v2/myapp/manifests/sha256:<digest>` → digest-based lookup
- `GET /v2/myapp/blobs/sha256:<digest>` with presign → 303 redirect
- `GET /v2/myapp/blobs/sha256:<digest>` without presign → 200 stream
- Missing image → 404 OCI JSON
- Missing blob → 404 OCI JSON
