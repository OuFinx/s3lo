# s3lo Roadmap

## v1.1.0 — Global Layer Deduplication

- [ ] Push blobs to global bucket-level store (`bucket/blobs/sha256/`)
- [ ] Pull blobs from global bucket-level store
- [ ] Backward compatible pull (support v1.0.0 and v1.1.0 layouts)
- [ ] `s3lo migrate` — convert v1.0.0 layout to v1.1.0
- [ ] `s3lo gc` — garbage collect unreferenced blobs
- [ ] `s3lo delete` — remove image tag
- [ ] S3 Intelligent-Tiering for blob storage
- [ ] Update list and inspect for v1.1.0 layout

## v1.2.0 — Lifecycle & Operations

- [ ] `s3lo lifecycle` — declarative retention policies
- [ ] `s3lo stats` — storage usage and deduplication savings
- [ ] `s3lo copy` — copy images between S3 buckets or from ECR
- [ ] `s3lo configure` — guided first-time setup
- [ ] Tag immutability
- [ ] S3 Lifecycle Rules recommendation generator
- [ ] Progress bars for push and pull

## v1.3.0 — Multi-Architecture Images

- [ ] Push multi-arch images with OCI Image Index
- [ ] Pull specific platform from multi-arch image
- [ ] Update inspect and list for multi-arch images

## v1.4.0 — CI Integration

- [ ] Official GitHub Action for s3lo push
- [ ] GitLab CI template

## v2.0.0 — Security & Documentation

- [ ] `s3lo sign` — sign images with cosign/Sigstore
- [ ] `s3lo verify` — verify image signatures
- [ ] Documentation website

## v2.1.0 — Vulnerability Scanning

- [ ] `s3lo scan` — vulnerability scanning with Trivy

## v2.2.0 — Multi-Cloud Support

- [ ] Abstract storage backend interface
- [ ] Google Cloud Storage backend
- [ ] MinIO / S3-compatible backend
