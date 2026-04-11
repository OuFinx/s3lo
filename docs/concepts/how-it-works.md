# How It Works

s3lo stores container images on S3 using the [OCI Image Layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) specification. Understanding the core design makes it easy to reason about performance, cost, and behavior.

## The core idea: content-addressed blobs

A container image is not a single file — it's a stack of layers plus a configuration blob. Each layer is a compressed tar archive of filesystem changes.

s3lo stores each blob as a separate S3 object, named by its SHA256 digest:

```
s3://my-bucket/blobs/sha256/a1b2c3d4e5f6...
```

This is called **content addressing**. Because the name is derived from the content, the same blob always has the same key — regardless of which image or tag references it. This enables:

- **Deduplication** — a blob that already exists is never uploaded again
- **Parallel transfers** — blobs have no dependency order, all download simultaneously
- **Integrity verification** — the SHA256 digest is checkable after download

## Push

```
Local Docker daemon
       │
       │ docker save (tar)
       ▼
   s3lo push
       │
       ├─ parse OCI layout from tar
       ├─ for each blob: HeadObject (does it exist?)
       │    ├─ yes → skip (dedup hit)
       │    └─ no  → PutObject (with Intelligent-Tiering)
       │
       └─ PutObject manifest.json + oci-layout
```

**What gets uploaded:**
- `blobs/sha256/<digest>` — each unique layer and config blob (S3 Intelligent-Tiering)
- `manifests/<image>/<tag>/manifest.json` — the OCI manifest (S3 Standard)
- `manifests/<image>/<tag>/oci-layout` — OCI layout marker (S3 Standard)

## Pull

```
s3lo pull
    │
    ├─ GetObject manifest.json
    ├─ (if multi-arch) select platform from index
    ├─ for each blob: GetObject in parallel
    │
    ├─ write OCI layout to temp dir on disk
    └─ docker load (imports into Docker daemon)
```

Blobs are downloaded concurrently. On EC2 with enhanced networking, this can saturate the network link — S3 has no per-connection limit, and instances like `c5n.18xlarge` support 100 Gbps.

## Copy (registry → S3)

`copy` bypasses the local Docker daemon entirely. It speaks the [OCI Distribution Spec](https://github.com/opencontainers/distribution-spec) API directly to pull manifests and blobs from the source registry, then uploads them to S3.

```
OCI Registry (Docker Hub, ECR, GHCR...)
       │
       │ GET /v2/<image>/manifests/<tag>
       │ GET /v2/<image>/blobs/<digest>  (parallel, up to 10)
       ▼
   s3lo copy
       │
       │ PutObject blobs/sha256/<digest>  (parallel, deduplicated)
       ▼
   S3 bucket
```

For multi-arch images, all platform manifests are fetched in parallel. Blobs shared across platforms (e.g. common base layers) are deduplicated and uploaded only once.

## S3 as a registry

S3 is not an OCI registry — it doesn't speak the Distribution Spec. s3lo uses the [OCI Image Layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md) format instead, which is a filesystem-based layout designed for this exact use case.

The trade-off: you can't point `docker pull` directly at S3. You use `s3lo pull`. For most workflows this is a drop-in replacement, with the advantage of significantly higher throughput inside AWS.
