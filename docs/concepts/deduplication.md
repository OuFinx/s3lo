# Deduplication

s3lo deduplicates at the blob level across all images in a bucket. This is the most effective level of deduplication for container images, because most images share large base layers.

## How it works

Before uploading any blob, s3lo checks if an object already exists at `blobs/sha256/<digest>`. If it exists, the upload is skipped entirely — no bytes are transferred.

```
Push myapp:v2.0
  └── ubuntu:22.04 layer  (200 MB) → HEAD blobs/sha256/a1b2... → exists → SKIP
  └── python:3.11 layer   (80 MB)  → HEAD blobs/sha256/e5f6... → exists → SKIP
  └── app-code-v2 layer   (6 MB)   → HEAD blobs/sha256/new1... → missing → UPLOAD
```

The SHA256 digest is the identity — same content always has the same digest, regardless of which image or tag it belongs to.

## Scope

Deduplication is **bucket-wide**. All images in the same bucket share the `blobs/sha256/` store. If you have 50 images all based on `ubuntu:22.04`, that 200 MB layer is stored exactly once.

## Real-world example

```
myapp:v1.0 layers:
  ubuntu-22.04      200 MB
  python-3.11        80 MB
  dependencies       50 MB
  app-code-v1         5 MB
  ─────────────────────────
  Total:            335 MB  → stored: 335 MB

myapp:v2.0 layers:
  ubuntu-22.04      200 MB  → already exists → 0 bytes uploaded
  python-3.11        80 MB  → already exists → 0 bytes uploaded
  dependencies       50 MB  → already exists → 0 bytes uploaded
  app-code-v2         6 MB  → new → 6 MB uploaded
  ─────────────────────────
  Total:            336 MB  → stored: 6 MB additional

After 10 releases:
  Logical size (what ECR would charge):  ~3.4 GB
  Actual stored:                         ~385 MB
  Savings:                               ~89%
```

This is why `s3lo stats` shows a "dedup savings" number — the difference between what a naive registry would store and what s3lo actually stores.

## Deduplication during copy

When copying a multi-arch image, s3lo deduplicates blobs **across platforms** as well. Many multi-arch images share base layers between `linux/amd64` and `linux/arm64`. These shared blobs are uploaded once regardless of how many platforms reference them.

## Effect on push time

After the first push, subsequent pushes of the same image (or a new version with unchanged layers) are extremely fast — only changed layers are uploaded. Pushing the exact same image twice uploads nothing except the manifest (a few KB).
