# s3lo Guide

Complete reference for all s3lo features, commands, and usage patterns.

---

## Table of Contents

- [How It Works](#how-it-works)
- [S3 Storage Layout](#s3-storage-layout)
- [Commands](#commands)
  - [push](#push)
  - [pull](#pull)
  - [list](#list)
  - [inspect](#inspect)
  - [delete](#delete)
  - [gc](#gc)
  - [migrate](#migrate)
  - [version](#version)
- [Authentication](#authentication)
- [IAM Policies](#iam-policies)
- [Storage Classes and Cost](#storage-classes-and-cost)
- [Deduplication](#deduplication)
- [Go Library Usage](#go-library-usage)
- [CI Integration](#ci-integration)
- [FAQ](#faq)

---

## How It Works

s3lo exports a Docker image using the Docker daemon, converts it to an OCI Image Layout, and stores each component as a separate S3 object. On pull, it does the reverse: downloads the components from S3 in parallel and imports the reassembled image into Docker.

The key insight is that every blob (image layer and config) is content-addressable by its SHA256 digest. This means:
- A blob is never uploaded twice. If two images share a layer, it is stored once.
- Downloads are parallelized - multiple blobs are fetched concurrently.
- Integrity is verifiable - SHA256 is checked after download.

---

## S3 Storage Layout

s3lo has two storage layouts. v1.1.0 is the current default. Both are fully supported.

### v1.1.0 Layout (current)

Blobs are stored globally, shared across all images in the bucket. Manifest metadata is stored per image tag.

```
s3://my-bucket/
  blobs/
    sha256/
      a1b2c3d4...         <- layer (stored with S3 Intelligent-Tiering)
      e5f6g7h8...         <- another layer (shared if used by multiple images)
      f0a1b2c3...         <- image config
  manifests/
    myapp/
      v1.0/
        manifest.json     <- OCI manifest (references blob digests)
        index.json        <- OCI image index
        oci-layout        <- OCI layout marker
    myapp/
      v2.0/
        manifest.json
        ...
    api/backend/
      latest/
        manifest.json
        ...
```

Benefits:
- Blobs are deduplicated bucket-wide. Two images sharing a base layer store it once.
- Blobs use S3 Intelligent-Tiering automatically - infrequently accessed layers move to cheaper storage tiers.
- Manifest metadata is cheap (tiny JSON files) and stays in S3 Standard.

### v1.0.0 Layout (legacy, still supported)

Everything was stored under a per-tag prefix. Each tag had its own copy of every blob.

```
s3://my-bucket/
  myapp/
    v1.0/
      manifest.json
      index.json
      oci-layout
      blobs/
        sha256/
          a1b2c3d4...     <- layer (per-tag copy)
          e5f6g7h8...
```

This layout is still fully readable by s3lo and s3lo-operator. Use `s3lo migrate` to convert to v1.1.0.

---

## Commands

### push

Push a local Docker image to S3.

```
s3lo push <local-image> <s3-ref>
```

**Arguments:**
- `<local-image>` - image name and tag as it appears in `docker images`, e.g. `myapp:v1.0`
- `<s3-ref>` - destination in `s3://bucket/image:tag` format

**What it does:**
1. Exports the image from the local Docker daemon as a tar archive.
2. Parses the OCI Image Layout from the archive.
3. Checks which blobs already exist in `blobs/sha256/` on S3 (deduplication check).
4. Uploads missing blobs in parallel to `blobs/sha256/<digest>` with S3 Intelligent-Tiering.
5. Uploads manifest files to `manifests/<image>/<tag>/`.

**Examples:**
```bash
# Push by tag
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

# Push to a nested image name (mirrors docker hub-style paths)
s3lo push org/backend:latest s3://my-bucket/org/backend:latest

# Push with an explicit version tag
s3lo push myapp:$(git rev-parse --short HEAD) s3://my-bucket/myapp:$(git rev-parse --short HEAD)
```

**Notes:**
- The local image must be available in Docker (`docker images` shows it). If it is not present, run `docker pull` first.
- On Apple Silicon (arm64), push `linux/amd64` images for EKS compatibility: `docker pull --platform linux/amd64 myapp:v1.0`.
- Pushing the same image twice is safe and fast - only changed or missing blobs are uploaded.

---

### pull

Download an image from S3 and import it into local Docker.

```
s3lo pull <s3-ref> [image-tag]
```

**Arguments:**
- `<s3-ref>` - source in `s3://bucket/image:tag` format
- `[image-tag]` - optional tag to apply after import (defaults to `image:tag` from the ref)

**What it does:**
1. Downloads `manifest.json` from S3 (tries v1.1.0 layout first, falls back to v1.0.0).
2. Downloads all blobs in parallel.
3. Reconstructs a local OCI Image Layout on disk.
4. Imports it into the local Docker daemon via `docker load`.
5. Optionally retags the image.

**Examples:**
```bash
# Pull and import with the same tag
s3lo pull s3://my-bucket/myapp:v1.0

# Pull and import with a custom local tag
s3lo pull s3://my-bucket/myapp:v1.0 myapp:local

# Pull a specific commit SHA
s3lo pull s3://my-bucket/myapp:abc1234
```

**Backward compatibility:**
s3lo detects which layout the image was stored in. If `manifests/<image>/<tag>/manifest.json` does not exist, it falls back to `<image>/<tag>/manifest.json` (v1.0.0). No configuration needed - this is automatic.

---

### list

List images stored in an S3 bucket.

```
s3lo list <s3-bucket-ref>
```

**Arguments:**
- `<s3-bucket-ref>` - bucket reference in `s3://bucket/` format

**What it does:**
Scans both `manifests/` prefix (v1.1.0 images) and the root prefix (v1.0.0 images, skipping `blobs` and `manifests` keys). Results are merged and deduplicated.

**Examples:**
```bash
# List all images in the bucket
s3lo list s3://my-bucket/

# Pipe to grep to filter
s3lo list s3://my-bucket/ | grep myapp
```

**Output format:**
```
myapp:v1.0
myapp:v2.0
api/backend:latest
org/frontend:sha-abc123
```

---

### inspect

Show image metadata including size, layers, and creation time.

```
s3lo inspect <s3-ref>
```

**Arguments:**
- `<s3-ref>` - image reference in `s3://bucket/image:tag` format

**What it does:**
Downloads and parses the OCI manifest and image config. Displays layers, total compressed size, architecture, OS, and creation timestamp.

**Examples:**
```bash
# Inspect a specific tag
s3lo inspect s3://my-bucket/myapp:v1.0

# Inspect to check architecture before deploying
s3lo inspect s3://my-bucket/myapp:v1.0 | grep -i arch
```

**Output example:**
```
Image:   s3://my-bucket/myapp:v1.0
Created: 2024-10-15T12:00:00Z
OS/Arch: linux/amd64
Layers:  4

  sha256:a1b2c3d4...  45.2 MB
  sha256:e5f6g7h8...  12.1 MB
  sha256:i9j0k1l2...   1.3 MB
  sha256:m3n4o5p6...   0.1 MB

Total: 58.7 MB
```

---

### delete

Remove an image tag from S3. Only removes the manifest files, not the blobs.

```
s3lo delete <s3-ref>
```

**Arguments:**
- `<s3-ref>` - image reference in `s3://bucket/image:tag` format

**What it does:**
Deletes all files under `manifests/<image>/<tag>/`. The blobs themselves are not touched - they remain in `blobs/sha256/` and may be referenced by other image tags. To clean up unreferenced blobs, run `s3lo gc` after deleting tags.

**Examples:**
```bash
# Delete a tag
s3lo delete s3://my-bucket/myapp:v1.0

# Delete and then garbage collect
s3lo delete s3://my-bucket/myapp:v1.0
s3lo gc s3://my-bucket/ --confirm
```

**Notes:**
- Deleting a tag that does not exist returns an error.
- Only works on v1.1.0 layout images. For v1.0.0 images, run `s3lo migrate` first.
- Deleting a tag that is still used by a running container does not affect the running container - the blobs remain available.

---

### gc

Garbage collect unreferenced blobs from the global blob store.

```
s3lo gc <s3-bucket-ref> [--confirm]
```

**Arguments:**
- `<s3-bucket-ref>` - bucket reference in `s3://bucket/` format

**Flags:**
- `--confirm` - actually delete unreferenced blobs. Without this flag, only a dry run is performed.

**What it does:**
1. Reads all manifests under `manifests/` to collect referenced blob digests.
2. Lists all keys under `blobs/sha256/`.
3. Compares the two sets.
4. Blobs not referenced by any manifest are candidates for deletion.
5. Blobs uploaded within the last hour are always skipped (grace period for in-progress pushes).
6. In dry-run mode, reports what would be deleted without making changes.
7. With `--confirm`, deletes the unreferenced blobs in batches.

**Examples:**
```bash
# Dry run - see what would be deleted
s3lo gc s3://my-bucket/

# Actually delete
s3lo gc s3://my-bucket/ --confirm
```

**Output (dry run):**
```
Dry run: 3 unreferenced blob(s) found (112.40 MB)
Run with --confirm to delete them.
```

**Output (confirmed):**
```
Deleted 3 blob(s), 112.40 MB freed
```

**When to run gc:**
- After `s3lo delete` to reclaim space from deleted tags.
- Periodically as a scheduled task if you push frequently and delete old tags.
- After `s3lo migrate` once you have verified the migration is complete.

**Safety:**
- The 1-hour grace period prevents a race condition where a blob is being pushed while gc runs. A blob pushed less than an hour ago will not be deleted even if no manifest references it yet.
- Always run without `--confirm` first to see what will be deleted.

---

### migrate

Convert images from v1.0.0 per-tag layout to v1.1.0 global blob layout.

```
s3lo migrate <s3-bucket-ref>
```

**Arguments:**
- `<s3-bucket-ref>` - bucket reference in `s3://bucket/` format

**What it does:**
1. Scans the bucket root for v1.0.0 image prefixes (directories that are not `blobs` or `manifests`).
2. For each image tag found:
   - Copies all blobs from `<image>/<tag>/blobs/sha256/` to `blobs/sha256/`.
   - Copies manifest files to `manifests/<image>/<tag>/`.
   - Deletes the old per-tag prefix.
3. Reports how many image tags and blobs were migrated.

**Examples:**
```bash
# Migrate the whole bucket
s3lo migrate s3://my-bucket/

# Safe to run again if interrupted
s3lo migrate s3://my-bucket/
```

**Output:**
```
Migrated 5 image tag(s), 23 blob(s) moved to global store
```

**Notes:**
- Idempotent - safe to run multiple times. CopyObject overwrites with the same content. If a tag was already migrated, it will be skipped (no old keys to delete).
- Both old and new layouts remain readable during and after migration - the fallback in `pull`, `inspect`, and `list` ensures continuity.
- Run `s3lo gc --confirm` after migration to clean up any blobs that were duplicated (same blob existed in multiple per-tag prefixes).
- The migration does not affect running containers or nodes using s3lo-operator.

---

### version

Print the s3lo version and build commit.

```
s3lo version
```

**Output:**
```
s3lo v1.1.0 (abc1234)
```

---

## Authentication

s3lo uses the standard AWS SDK credential chain. It does not have its own authentication mechanism. Credentials are resolved in this order:

1. `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` environment variables
2. `AWS_PROFILE` environment variable (selects a named profile)
3. `~/.aws/credentials` and `~/.aws/config` (default profile)
4. IAM instance profile (EC2, ECS task role, etc.)
5. EKS Pod Identity / IRSA

**Region:**
s3lo auto-detects the bucket region using `s3:GetBucketLocation`. You do not need to specify a region. The environment variable `AWS_DEFAULT_REGION` is respected if set.

**Using named profiles:**
```bash
AWS_PROFILE=prod s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
AWS_PROFILE=staging s3lo list s3://staging-bucket/
```

**SSO:**
```bash
aws sso login --profile prod
AWS_PROFILE=prod s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

---

## IAM Policies

### Full access (push + pull + manage)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:HeadObject",
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": [
        "arn:aws:s3:::my-bucket",
        "arn:aws:s3:::my-bucket/*"
      ]
    }
  ]
}
```

Required for: `push`, `delete`, `gc`, `migrate`.

### Read-only (pull only)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:HeadObject",
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": [
        "arn:aws:s3:::my-bucket",
        "arn:aws:s3:::my-bucket/*"
      ]
    }
  ]
}
```

Required for: `pull`, `list`, `inspect`.

### Per-command breakdown

| Command | Required S3 Actions |
|---------|---------------------|
| push | GetObject, PutObject, HeadObject, ListBucket, GetBucketLocation |
| pull | GetObject, HeadObject, ListBucket, GetBucketLocation |
| list | ListBucket, GetBucketLocation |
| inspect | GetObject, GetBucketLocation |
| delete | GetObject, DeleteObject, ListBucket, GetBucketLocation |
| gc | GetObject, DeleteObject, ListBucket, GetBucketLocation |
| migrate | GetObject, PutObject, DeleteObject, ListBucket, GetBucketLocation |

---

## Storage Classes and Cost

### How blobs are stored

When s3lo pushes an image, it stores blobs (layers and config) using **S3 Intelligent-Tiering**. This storage class automatically moves objects between access tiers based on access patterns:

- **Frequent Access tier** - objects accessed recently (standard S3 pricing)
- **Infrequent Access tier** - objects not accessed for 30 days (40% cost reduction)
- **Archive Instant Access tier** - objects not accessed for 90 days (68% cost reduction)
- **Archive Access tier** - optional, for objects not accessed for 90+ days (71% cost reduction)
- **Deep Archive Access tier** - optional, for objects not accessed for 180+ days (95% cost reduction)

Manifest files (small JSON, accessed on every pull) are stored in **S3 Standard** to avoid Intelligent-Tiering's per-object monitoring fee on tiny files.

### Cost comparison

| Storage Class | Per GB/month | Best for |
|---------------|--------------|----------|
| S3 Standard | $0.023 | Manifests, frequently pulled images |
| S3 Intelligent-Tiering | $0.023 (frequent) to $0.004 (deep archive) | Blobs - automatic tiering |
| ECR | $0.10 | (comparison baseline) |

For a typical repository where most image versions are rarely pulled after the first month, Intelligent-Tiering reduces blob storage cost by 40-95% compared to S3 Standard, and 60-99% compared to ECR.

### Deduplication additional savings

With v1.1.0 global blob deduplication, every blob is stored once regardless of how many image tags reference it. If your images share a large base layer (e.g. a 200 MB Ubuntu layer), that 200 MB is stored and billed once, not once per tag.

---

## Deduplication

s3lo deduplicates at the blob level using SHA256 content addressing.

**How it works:**
1. Before uploading a blob, s3lo checks if the key `blobs/sha256/<digest>` already exists in S3.
2. If it exists, the upload is skipped entirely.
3. If it does not exist, it is uploaded.

This means:
- Two different image tags with the same base layer store that layer once.
- Rebuilding an image with only code changes does not re-upload unchanged layers (OS layer, dependency layer, etc.).
- Pushing the same tag twice uploads nothing if the image is unchanged.

**Scope:**
Deduplication is bucket-wide. All images in the same bucket share the `blobs/sha256/` store.

**Example:**
```
myapp:v1.0 has layers: [ubuntu-22.04, python-3.11, app-code-v1]
myapp:v2.0 has layers: [ubuntu-22.04, python-3.11, app-code-v2]

Push v1.0: uploads ubuntu-22.04 (200 MB), python-3.11 (80 MB), app-code-v1 (5 MB)
Push v2.0: skips ubuntu-22.04, skips python-3.11, uploads app-code-v2 (6 MB)

Storage used: 291 MB instead of 572 MB
```

---

## Go Library Usage

s3lo exposes its internal packages. Import them to build tools that push, pull, or inspect images programmatically.

### Available packages

```go
import (
    "github.com/OuFinx/s3lo/pkg/ref"   // Parse s3:// references
    "github.com/OuFinx/s3lo/pkg/oci"   // OCI manifest and image config types
    "github.com/OuFinx/s3lo/pkg/s3"    // S3 client with region auto-detection
    "github.com/OuFinx/s3lo/pkg/image" // Push, pull, list, inspect, delete, gc, migrate
)
```

All functions accept `context.Context` as the first argument for cancellation and deadline support.

### Push an image

```go
package main

import (
    "context"
    "log"

    "github.com/OuFinx/s3lo/pkg/image"
)

func main() {
    ctx := context.Background()
    if err := image.Push(ctx, "myapp:v1.0", "s3://my-bucket/myapp:v1.0"); err != nil {
        log.Fatal(err)
    }
}
```

### Pull an image

```go
// Pull and import into local Docker daemon
if err := image.Pull(ctx, "s3://my-bucket/myapp:v1.0", "myapp:v1.0"); err != nil {
    log.Fatal(err)
}
```

### List images

```go
images, err := image.List(ctx, "s3://my-bucket/")
if err != nil {
    log.Fatal(err)
}
for _, img := range images {
    fmt.Println(img)
}
```

### Inspect metadata

```go
meta, err := image.Inspect(ctx, "s3://my-bucket/myapp:v1.0")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("arch: %s, layers: %d\n", meta.Architecture, len(meta.Layers))
```

### Garbage collect

```go
result, err := image.GC(ctx, "s3://my-bucket/", false) // true = dry run
if err != nil {
    log.Fatal(err)
}
fmt.Printf("deleted %d blobs, freed %d bytes\n", result.Deleted, result.FreedBytes)
```

### Migrate

```go
result, err := image.Migrate(ctx, "s3://my-bucket/")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("migrated %d images, %d blobs moved\n", result.Images, result.BlobsMoved)
```

### Parse a reference

```go
r, err := ref.Parse("s3://my-bucket/myapp:v1.0")
if err != nil {
    log.Fatal(err)
}
fmt.Println(r.Bucket) // "my-bucket"
fmt.Println(r.Image)  // "myapp"
fmt.Println(r.Tag)    // "v1.0"
```

---

## CI Integration

### GitHub Actions

```yaml
name: Build and Push

on:
  push:
    branches: [main]

jobs:
  push:
    runs-on: ubuntu-latest
    permissions:
      id-token: write  # for OIDC to assume IAM role
      contents: read

    steps:
      - uses: actions/checkout@v4

      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Build image
        run: docker build --platform linux/amd64 -t myapp:${{ github.sha }} .

      - name: Install s3lo
        run: |
          curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
          tar xzf s3lo.tar.gz && chmod +x s3lo && sudo mv s3lo /usr/local/bin/

      - name: Push image to S3
        run: s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:${{ github.sha }}

      - name: Tag as latest
        run: s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:latest
```

### GitLab CI

```yaml
stages:
  - build
  - push

variables:
  IMAGE_NAME: myapp
  S3_BUCKET: my-bucket

push-to-s3:
  stage: push
  image: docker:24
  services:
    - docker:24-dind
  before_script:
    - apk add --no-cache curl tar
    - curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
    - tar xzf s3lo.tar.gz && chmod +x s3lo && mv s3lo /usr/local/bin/
  script:
    - docker build --platform linux/amd64 -t $IMAGE_NAME:$CI_COMMIT_SHA .
    - s3lo push $IMAGE_NAME:$CI_COMMIT_SHA s3://$S3_BUCKET/$IMAGE_NAME:$CI_COMMIT_SHA
```

### Scheduled garbage collection

A simple cron job or scheduled CI workflow to periodically reclaim space:

```yaml
# GitHub Actions scheduled workflow
name: GC

on:
  schedule:
    - cron: '0 3 * * *'  # daily at 3 AM

jobs:
  gc:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read
    steps:
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Install s3lo
        run: |
          curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
          tar xzf s3lo.tar.gz && chmod +x s3lo && sudo mv s3lo /usr/local/bin/

      - name: Run GC
        run: s3lo gc s3://my-bucket/ --confirm
```

---

## FAQ

**Q: Can I use s3lo without Docker?**

Not currently. s3lo uses the Docker daemon to export images (`docker save`) and to import them (`docker load`). Docker must be running locally. Future versions may support direct OCI tarball operations without Docker.

**Q: Does s3lo support multi-architecture images?**

Not yet. Multi-arch support (OCI Image Index) is planned for v1.3.0. Currently, each image reference stores a single-platform image. Best practice for EKS is to push `linux/amd64` explicitly.

**Q: What happens if a push is interrupted?**

Blobs uploaded before the interruption remain in S3. Re-running the push will skip those blobs and continue from where it left off. The manifest files are uploaded last - if the push is interrupted before the manifest is written, the image will not appear in `s3lo list` but the orphaned blobs will be cleaned up by `s3lo gc`.

**Q: Can two pushes run in parallel to the same tag?**

This is a race condition. The last manifest to be written wins. Blob uploads are safe to parallelize (same content, same key), but two simultaneous pushes of different images could result in an inconsistent state if they interleave. It is best to serialize pushes to the same tag.

**Q: How do I delete all tags for an image?**

Run `s3lo list` to find all tags, then delete each one:
```bash
s3lo list s3://my-bucket/ | grep "myapp:" | while read ref; do
  s3lo delete "s3://my-bucket/$ref"
done
s3lo gc s3://my-bucket/ --confirm
```

**Q: Is there a way to copy an image between buckets?**

`s3lo copy` is planned for v1.2.0. Until then, you can pull to local Docker and push to the destination bucket:
```bash
s3lo pull s3://source-bucket/myapp:v1.0 myapp:v1.0
s3lo push myapp:v1.0 s3://dest-bucket/myapp:v1.0
```

**Q: Why does pull show "image imported into Docker" but `docker images` shows nothing?**

The import uses `docker load` which should appear immediately. Check if Docker is running and if there are errors in the output. Also verify the image was pushed with the correct platform (`linux/amd64` for non-ARM hosts).

**Q: Does s3lo work with S3-compatible storage (MinIO, Backblaze, etc.)?**

Not officially yet. MinIO and other S3-compatible backends are planned for v2.2.0. The AWS SDK's endpoint override (`AWS_ENDPOINT_URL`) may work for testing, but this is unsupported.

**Q: How do I see s3lo version?**

```bash
s3lo version
```

**Q: Can I use s3lo without AWS CLI installed?**

Yes. s3lo uses the AWS SDK directly and does not require the AWS CLI. You only need valid AWS credentials (environment variables, `~/.aws/credentials`, or an IAM role).

**Q: What is the maximum image size?**

There is no hard limit in s3lo. S3 supports objects up to 5 TB. s3lo uploads each blob as a single S3 PutObject, which supports up to 5 GB per blob. For images with blobs larger than 5 GB, multipart upload would be needed (not currently implemented). In practice, individual image layers are rarely larger than a few hundred MB.

**Q: How do I migrate from ECR to S3?**

```bash
# Pull from ECR, push to S3
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 123456789.dkr.ecr.us-east-1.amazonaws.com
docker pull 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0
s3lo push 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

**Q: What does the 1-hour gc grace period protect against?**

If a push is in progress and gc runs at the same time, blobs that have been uploaded but whose manifest has not been written yet would be incorrectly identified as unreferenced. The grace period ensures blobs uploaded in the last hour are never deleted by gc, even if no manifest references them yet. This eliminates the race condition.
