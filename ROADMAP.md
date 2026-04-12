# s3lo Roadmap

## v1.1.0 — Global Layer Deduplication ✓

- [x] Push blobs to global bucket-level store (`bucket/blobs/sha256/`)
- [x] Pull blobs from global bucket-level store
- [x] Backward compatible pull (support v1.0.0 and v1.1.0 layouts)
- [x] `s3lo migrate` — convert v1.0.0 layout to v1.1.0
- [x] `s3lo gc` — garbage collect unreferenced blobs
- [x] `s3lo delete` — remove image tag
- [x] S3 Intelligent-Tiering for blob storage
- [x] Update list and inspect for v1.1.0 layout

## v1.2.0 — Lifecycle & Operations ✓

- [x] `s3lo clean` — prune old tags + GC unreferenced blobs in one command
- [x] `s3lo stats` — storage usage and deduplication savings
- [x] `s3lo copy` — copy images between S3 buckets or from ECR/OCI registries
- [x] Per-image immutability and lifecycle config stored in bucket (`s3lo.yaml`)
- [x] `s3lo config set/get/remove` — manage per-image and bucket-wide config
- [x] `s3lo config recommend` — data-driven bucket analysis and recommendations
- [x] Progress output for push and pull

## v1.3.0 — Multi-Architecture Images ✓

- [x] `s3lo copy` copies all platforms by default for multi-arch images (OCI Image Index)
- [x] `s3lo pull` auto-detects host platform; `--platform` flag to override
- [x] `s3lo inspect` displays per-platform details for multi-arch images
- [x] `s3lo copy --platform` to copy a single platform from a multi-arch image

## v1.4.0 — CI Integration & Documentation ✓

- [x] Official GitHub Action for s3lo push ([OuFinx/s3lo-action](https://github.com/OuFinx/s3lo-action))
- [x] Documentation website (MkDocs Material, auto-deployed to GitHub Pages)

## v1.5.0 — Vulnerability Scanning

- [x] `s3lo scan` — vulnerability scanning with Trivy
- [x] Auto-install Trivy when not found (Y/N prompt, `--install-trivy` flag)
- [x] Multi-arch support: `--platform` flag to select platform
- [x] Severity filtering: `--severity HIGH,CRITICAL`
- [x] Output format control: `--format json|sarif|cyclonedx`

## v1.6.0 — Code Quality & Reliability

- [x] Refactor: split copy.go into focused files (copy_s3.go, copy_registry.go, registry_auth.go, registry_ref.go)
- [x] Performance: stream registry blobs directly to temp files (no in-memory buffer)
- [x] Performance: cache Bearer token per registry, skip repeated 401 round-trips
- [x] Reliability: retry registry HTTP calls on transient errors with exponential backoff
- [x] UX: deterministic progress bar showing transferred / total bytes for push, pull, copy, scan
- [x] Observability: `--verbose` flag with `slog` debug output for HTTP requests, auth, retries

## v2.0.0 — Security

- [ ] `s3lo sign` — sign images with cosign/Sigstore
- [ ] `s3lo verify` — verify image signatures

## v2.1.0 — Multi-Cloud Support

- [ ] Abstract storage backend interface
- [ ] Google Cloud Storage backend
- [ ] MinIO / S3-compatible backend
