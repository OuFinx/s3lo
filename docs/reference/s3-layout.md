# S3 Layout Specification

This page is the authoritative reference for s3lo's S3 storage format.

## Bucket structure

```
s3://bucket/
├── blobs/
│   └── sha256/
│       └── <hex-digest>        OCI blob (layer or config)
├── manifests/
│   └── <image-name>/
│       └── <tag>/
│           ├── manifest.json   OCI Manifest or OCI Image Index
│           └── oci-layout      OCI Image Layout marker
└── s3lo.yaml                   Bucket configuration
```

## Blob objects

**Key:** `blobs/sha256/<hex-digest>`

- `<hex-digest>` is the lowercase hex SHA256 digest of the blob content (without `sha256:` prefix)
- Content: raw compressed blob data (gzip-compressed tar for layers; JSON for configs)
- Storage class: `INTELLIGENT_TIERING`
- Content-Type: `application/octet-stream`

## Manifest objects

**Key:** `manifests/<image>/<tag>/manifest.json`

- `<image>` may contain `/` for namespaced images (e.g. `org/backend`)
- Content: OCI Manifest JSON (single-arch) or OCI Image Index JSON (multi-arch)
- Storage class: `STANDARD`

**Key:** `manifests/<image>/<tag>/oci-layout`

- Content: `{"imageLayoutVersion":"1.0.0"}`
- Storage class: `STANDARD`

## Manifest format

### Single-arch (OCI Manifest)

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {
    "mediaType": "application/vnd.oci.image.config.v1+json",
    "digest": "sha256:abc123...",
    "size": 1234
  },
  "layers": [
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
      "digest": "sha256:def456...",
      "size": 52428800
    }
  ]
}
```

### Multi-arch (OCI Image Index)

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.index.v1+json",
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:amd64manifest...",
      "size": 528,
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:arm64manifest...",
      "size": 528,
      "platform": {
        "architecture": "arm64",
        "os": "linux"
      }
    }
  ]
}
```

For multi-arch images, the per-platform manifests are stored as blobs (`blobs/sha256/<platform-manifest-digest>`), not as separate manifest objects.

## Configuration file

**Key:** `s3lo.yaml`

```yaml
default:
  immutable: false
  lifecycle:
    keep_last: 10
    max_age: 90d
    keep_tags: []

images:
  myapp:
    immutable: true
    lifecycle:
      keep_last: 5
      keep_tags:
        - stable
        - latest
  dev/*:
    lifecycle:
      max_age: 7d
      keep_last: 3
```

## Versioning

This layout is s3lo v1.1.0+. Use `s3lo migrate` to convert buckets from the v1.0.0 per-image blob layout.
