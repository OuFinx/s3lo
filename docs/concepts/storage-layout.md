# Storage Layout

s3lo stores images in a well-defined layout inside your S3 bucket or local directory. Understanding the layout makes it easy to reason about costs, permissions, and interoperability.

The layout is identical for S3 (`s3://`) and local storage (`local://`).

## Top-level structure

```
s3://my-bucket/
├── blobs/
│   └── sha256/
│       ├── a1b2c3d4...     ← layer blob
│       ├── e5f6g7h8...     ← another layer blob
│       └── f0a1b2c3...     ← image config blob
├── manifests/
│   ├── myapp/
│   │   ├── v1.0/
│   │   │   ├── manifest.json
│   │   │   └── oci-layout
│   │   └── v2.0/
│   │       ├── manifest.json
│   │       └── oci-layout
│   └── org/backend/
│       └── latest/
│           ├── manifest.json
│           └── oci-layout
└── s3lo.yaml               ← bucket config (lifecycle, immutability)
```

## Blobs

All blobs (layers and configs) are stored globally under `blobs/sha256/`. They are **shared across all images in the bucket** — if two images use the same base layer, it is stored once.

- **Key:** `blobs/sha256/<sha256-digest-hex>`
- **Storage class:** S3 Intelligent-Tiering (auto-moves to cheaper tiers as access frequency drops)
- **Content:** raw compressed layer data or image config JSON

## Manifests

Manifest metadata is stored per image tag under `manifests/<image>/<tag>/`.

| File | Description |
|------|-------------|
| `manifest.json` | OCI Manifest (single-arch) or OCI Image Index (multi-arch). References blobs by digest. |
| `oci-layout` | `{"imageLayoutVersion":"1.0.0"}` — marks this as an OCI Image Layout |

- **Storage class:** S3 Standard (small files, accessed on every pull — Intelligent-Tiering's per-object fee would be wasteful)

## Multi-arch layout

For multi-arch images, `manifest.json` is an OCI Image Index that references per-platform manifests. The per-platform manifests are stored as blobs (not in `manifests/`):

```
manifests/alpine/latest/
  manifest.json       ← OCI Image Index (lists platforms)

blobs/sha256/
  <index-digest>      ← (not used, index is at manifest.json)
  <amd64-manifest>    ← OCI Manifest for linux/amd64
  <arm64-manifest>    ← OCI Manifest for linux/arm64
  <amd64-layer-1>     ← layer (may be shared between platforms)
  <arm64-layer-1>     ← platform-specific layer
  ...
```

## Configuration

`s3lo.yaml` at the bucket root stores per-image lifecycle rules and immutability settings. It is a small YAML file read by `push` (for immutability checks) and `clean` (for lifecycle rules).

## Why this layout?

- **Global blob store** — enables bucket-wide deduplication without any metadata overhead
- **OCI Image Layout** — a standard format; the layout is portable and interoperable with other OCI tools
- **S3 Intelligent-Tiering for blobs** — layers that haven't been pulled recently automatically move to cheaper tiers
- **S3 Standard for manifests** — manifests are tiny (a few KB) and accessed on every pull, so the fixed per-object monitoring fee of Intelligent-Tiering is not worth it
