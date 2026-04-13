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

## v1.7.0 — UX & Output Formats

- [x] `--output json|yaml|table` flag on `list`, `inspect`, `stats`, `config get`
- [x] Enhanced cost comparison in `stats`: S3 current, S3 no-dedup, ECR equivalent, savings vs ECR

## v1.8.0 — Reliability & Operations

- [x] Multipart upload for blobs >100 MB (removes 5 GB hard limit, uses 64 MB parts with abort on failure)
- [x] `s3lo history` — push history per tag (timestamp, digest, size; stored in `history.json`)
- [x] `s3lo doctor` — bucket health check: layout, manifest integrity, orphaned blobs, config validity
- [x] `s3lo init` — bucket initialization: verify access, check Intelligent-Tiering, write default `s3lo.yaml`
- [x] `local://` storage backend — all commands work against a local directory without AWS

## v1.9.0 — Security

- [x] `s3lo sign` — sign images with cosign/Sigstore (AWS KMS, local key, HashiCorp Vault)
- [x] `s3lo verify` — verify image signatures (exit 0/1/2 for CI gates)

## v1.10.0 — Multi-Cloud Support ✓

- [x] Google Cloud Storage backend (`gs://`)
- [x] Azure Blob Storage backend (`az://`)
- [x] MinIO / S3-compatible backend (`--endpoint` flag for MinIO, Cloudflare R2, Ceph, etc.)
- [x] `pkg/s3` renamed to `pkg/storage` with decoupled `StorageClass` type

## v1.11.0 — Policy & SBOM

- [x] `s3lo config validate` — policy rules and compliance checks
- [x] `s3lo sbom` — generate Software Bill of Materials

## v1.12.0 — Long-Term Vision

- [ ] `s3lo serve` — OCI Distribution Spec proxy over S3

## v1.13.0

- [ ] Interactive TUI for browsing images and managing lifecycle

## v2.0.0 — Stable Release

- [ ] Final API and CLI stability review
- [ ] Full documentation pass
- [ ] Official stable release
