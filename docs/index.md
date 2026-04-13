# s3lo

**Use object storage as a container image registry.** AWS S3, Google Cloud Storage, Azure Blob, MinIO, Cloudflare R2 — faster pulls, cheaper storage, no registry to manage.

```bash
# Push to S3
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

# Push to GCS
s3lo push myapp:v1.0 gs://my-gcs-bucket/myapp:v1.0

# Push to Azure Blob
s3lo push myapp:v1.0 az://my-container/myapp:v1.0

# Push to MinIO or Cloudflare R2
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --endpoint http://localhost:9000

# Mirror any image from Docker Hub, ECR, or GHCR directly to storage
s3lo copy alpine:latest s3://my-bucket/alpine:latest
```

---

## Why s3lo?

Container registries are a solved problem — but ECR and Docker Hub are generic solutions. If you're already running on AWS, your S3 bucket is right next to your EC2 instances. Why go through a registry?

| | ECR | s3lo + S3 |
|---|---|---|
| **Pull speed (EC2)** | ~1–5 Gbps | Up to 100 Gbps |
| **Storage cost** | $0.10/GB/month | $0.023/GB/month |
| **Deduplication** | None | Bucket-wide, SHA256 |
| **Multi-arch** | Yes | Yes (OCI Image Index) |
| **Registry to manage** | Lifecycle policies, permissions, replication | Just a bucket |
| **Auth** | ECR token (expires in 12h) | Standard cloud credentials |
| **Cloud support** | AWS only | AWS, GCP, Azure, MinIO, R2, Ceph |

S3 has no concept of a "registry" — s3lo stores images using the [OCI Image Layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) format, with each layer as a separate object. Layers are content-addressed by SHA256, so they're never uploaded twice.

---

## How it works in 30 seconds

```
Your Docker daemon  ──push──►  s3://my-bucket/
                                  blobs/sha256/
                                    a1b2c3d4...  ← layer (shared by all images)
                                    e5f6g7h8...  ← layer
                                  manifests/
                                    myapp/v1.0/
                                      manifest.json
```

**Push:** s3lo exports the image from Docker, splits it into content-addressable blobs, and uploads only the blobs that don't already exist in S3.

**Pull:** s3lo downloads all blobs in parallel and imports the reassembled image into Docker. On EC2 with enhanced networking, this can reach 100 Gbps — limited by the instance, not the registry.

**Copy:** s3lo pulls directly from any OCI registry (Docker Hub, ECR, GHCR) and uploads to S3 — without going through the local Docker daemon.

---

## Quick install

=== "macOS / Linux (recommended)"

    ```bash
    curl -sSL https://raw.githubusercontent.com/OuFinx/s3lo/main/install.sh | sh
    ```

=== "Homebrew"

    ```bash
    brew install OuFinx/tap/s3lo
    ```

=== "Go"

    ```bash
    go install github.com/OuFinx/s3lo/cmd/s3lo@latest
    ```

---

## Supported backends

| Scheme | Backend | Auth |
|--------|---------|------|
| `s3://` | AWS S3 | Standard AWS credentials |
| `s3://` + `--endpoint` | MinIO / Cloudflare R2 / Ceph | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` |
| `gs://` | Google Cloud Storage | Application Default Credentials |
| `az://` | Azure Blob Storage | DefaultAzureCredential + `AZURE_STORAGE_ACCOUNT` |
| `local://` | Local filesystem | None |

## Local storage

No cloud account? Use `local://` to store images on your filesystem:

```bash
s3lo init --local ./local-s3
s3lo push myapp:v1.0 local://./local-s3/myapp:v1.0
s3lo pull local://./local-s3/myapp:v1.0
```

Local storage uses the same OCI layout as all other backends. All commands work identically across schemes.

---

## Next steps

- [**Getting Started**](getting-started.md) — install, create a bucket, push your first image
- [**How It Works**](concepts/how-it-works.md) — architecture deep dive
- [**GitHub Actions**](guides/github-actions.md) — CI/CD integration with OIDC
- [**ECR Migration**](guides/ecr-migration.md) — move your images from ECR to S3
