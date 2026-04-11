# s3lo Guide

Complete reference for all s3lo features, commands, and usage patterns.

---

## Table of Contents

- [How It Works](#how-it-works)
- [S3 Storage Layout](#s3-storage-layout)
- [Commands](#commands)
  - [push](#push)
  - [pull](#pull)
  - [copy](#copy)
  - [list](#list)
  - [inspect](#inspect)
  - [delete](#delete)
  - [clean](#clean)
  - [stats](#stats)
  - [config](#config)
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
- Downloads are parallelized — multiple blobs are fetched concurrently.
- Integrity is verifiable — SHA256 is checked after download.

---

## S3 Storage Layout

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
      v2.0/
        manifest.json
        ...
    api/backend/
      latest/
        manifest.json
        ...
  s3lo.yaml               <- bucket config (immutability, lifecycle rules)
```

Benefits:
- Blobs are deduplicated bucket-wide. Two images sharing a base layer store it once.
- Blobs use S3 Intelligent-Tiering automatically — infrequently accessed layers move to cheaper storage tiers.
- Manifest metadata is cheap (tiny JSON files) and stays in S3 Standard.

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

**Flags:**
- `--force` - overwrite an existing tag even if the image has immutability enabled

**What it does:**
1. Exports the image from the local Docker daemon as a tar archive.
2. Parses the OCI Image Layout from the archive.
3. Checks which blobs already exist in `blobs/sha256/` on S3 (deduplication check).
4. Uploads missing blobs to `blobs/sha256/<digest>` with S3 Intelligent-Tiering.
5. Uploads manifest files to `manifests/<image>/<tag>/`.

**Examples:**
```bash
# Push by tag
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

# Push to a nested image name
s3lo push org/backend:latest s3://my-bucket/org/backend:latest

# Push with a git commit SHA tag
s3lo push myapp:$(git rev-parse --short HEAD) s3://my-bucket/myapp:$(git rev-parse --short HEAD)

# Force-overwrite an immutable tag
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --force
```

**Progress output:**
```
Pushing myapp:v1.0 to s3://my-bucket/myapp:v1.0
  uploading ⠸ 58.7 MB [45s]
Done.
```

**Notes:**
- The local image must be available in Docker (`docker images`). If not, run `docker pull` first.
- On Apple Silicon (arm64), push `linux/amd64` images for EKS compatibility: `docker pull --platform linux/amd64 myapp:v1.0`.
- Pushing the same image twice is safe and fast — only changed or missing blobs are uploaded.
- If the image has immutability enabled, pushing to an existing tag fails unless `--force` is passed.

---

### pull

Download an image from S3 and import it into local Docker.

```
s3lo pull <s3-ref> [image-tag] [flags]
```

**Arguments:**
- `<s3-ref>` - source in `s3://bucket/image:tag` format
- `[image-tag]` - optional tag to apply after import (defaults to `image:tag` from the ref)

**Flags:**
- `--platform <os/arch>` - select a specific platform from a multi-arch image (e.g. `linux/amd64`). Default: auto-detect host platform.

**What it does:**
1. Downloads `manifest.json` from S3.
2. If the image is a multi-arch index, selects the platform matching the host (or `--platform`).
3. Downloads all blobs in parallel.
4. Reconstructs a local OCI Image Layout on disk.
5. Imports it into the local Docker daemon via `docker load`.
6. Optionally retags the image.

**Examples:**
```bash
# Pull and import (auto-detect platform for multi-arch images)
s3lo pull s3://my-bucket/myapp:v1.0

# Pull with a custom local tag
s3lo pull s3://my-bucket/myapp:v1.0 myapp:local

# Pull a specific platform from a multi-arch image
s3lo pull s3://my-bucket/myapp:v1.0 --platform linux/arm64
```

**Progress output:**
```
Pulling s3://my-bucket/myapp:v1.0
  downloading ⠸ 58.7 MB [30s]
Done. Image imported into Docker.
```

---

### copy

Copy an image to S3 without pulling it to the local Docker daemon.

```
s3lo copy <src> <s3-dest> [flags]
```

**Arguments:**
- `<src>` - source image. Can be an S3 reference or an OCI registry reference.
- `<s3-dest>` - destination in `s3://bucket/image:tag` format.

**Flags:**
- `--platform <os/arch>` - copy a specific platform only (e.g. `linux/amd64`). Default: copy all platforms.

**Source formats supported:**

| Source | Example |
|--------|---------|
| S3 bucket | `s3://source-bucket/myapp:v1.0` |
| ECR | `123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0` |
| Docker Hub | `docker.io/library/nginx:latest` |
| Any OCI registry | `registry.example.com/myapp:v1.0` |

For multi-arch images, all platforms are copied by default and the full OCI Image Index is preserved at the destination. Use `--platform` to copy only one platform.

**Examples:**
```bash
# Copy from Docker Hub — bare name, same as docker pull
s3lo copy alpine:latest s3://my-bucket/alpine:latest
s3lo copy nginx:1.25 s3://my-bucket/nginx:1.25

# Copy a specific platform only
s3lo copy alpine:latest s3://my-bucket/alpine:latest --platform linux/amd64

# Copy from ECR to S3 (auto-authenticates using your AWS credentials)
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0

# Copy from GHCR or any OCI registry
s3lo copy ghcr.io/owner/myapp:v1.0 s3://my-bucket/myapp:v1.0

# Copy between S3 buckets (all platforms preserved, server-side within same bucket)
s3lo copy s3://source-bucket/myapp:v1.0 s3://dest-bucket/myapp:v1.0
```

**Reference formats accepted:**

| Format | Example | Resolves to |
|--------|---------|-------------|
| Bare name | `alpine` | `docker.io/library/alpine:latest` |
| Name:tag | `alpine:3.18` | `docker.io/library/alpine:3.18` |
| User/image | `user/myapp:v1.0` | `docker.io/user/myapp:v1.0` |
| Full registry | `ghcr.io/owner/image:tag` | as-is |
| S3 | `s3://bucket/image:tag` | as-is |

**How it works:**

*S3-to-S3 (same bucket):*
Uses S3 server-side `CopyObject` for blobs — no data transfer, no cost, instant.

*S3-to-S3 (different buckets):*
Downloads each blob to a local temp file, uploads to the destination bucket.

*ECR-to-S3:*
Calls `ecr:GetAuthorizationToken` using your AWS credentials to get a temporary token, then pulls manifests and blobs from the ECR registry API and uploads them to S3.

*Other OCI registries:*
Attempts unauthenticated access first. On 401, performs the standard Bearer token challenge flow (`WWW-Authenticate` header).

**Blob deduplication:**
Before uploading each blob, copy checks whether it already exists at the destination. Existing blobs are skipped.

**Output:**
```
Copying alpine:latest to s3://my-bucket/alpine:latest
  copying ⠸ 47.3 MB [1m05s]
Done. 16 platform(s) copied, 23 blob(s) copied, 32 skipped (already exist).
```

**IAM requirements for ECR source:**
```json
{
  "Effect": "Allow",
  "Action": ["ecr:GetAuthorizationToken"],
  "Resource": "*"
}
```
Plus read access to the ECR repository.

---

### list

List images stored in an S3 bucket.

```
s3lo list <s3-bucket-ref>
```

**Examples:**
```bash
s3lo list s3://my-bucket/
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

Show image metadata including size, layers, and platform information.

```
s3lo inspect <s3-ref>
```

Supports both single-arch images and multi-arch image indexes.

**Examples:**
```bash
s3lo inspect s3://my-bucket/myapp:v1.0
s3lo inspect s3://my-bucket/alpine:latest
```

**Single-arch output:**
```
Reference: s3://my-bucket/myapp:v1.0
Type:      single-arch image
Layers:    4
Total:     58.70 MB

  [1] sha256:a1b2c3d4e5f67890...  (45.20 MB)
  [2] sha256:e5f6g7h8i9j01234...  (12.10 MB)
  [3] sha256:i9j0k1l2m3n45678...   (1.30 MB)
  [4] sha256:m3n4o5p6q7r89012...   (0.10 MB)
```

**Multi-arch output:**
```
Reference: s3://my-bucket/alpine:latest
Type:      multi-arch image index (3 platform(s))

  Platform: linux/amd64
  Digest:   sha256:a1b2c3d4e5f...
  Layers:   4
  Size:     7.80 MB
    [1] sha256:a1b2c3d4...  (3.20 MB)
    ...

  Platform: linux/arm64
  ...

  Platform: linux/arm/v7
  ...
```

---

### delete

Remove an image tag from S3.

```
s3lo delete <s3-ref>
```

**What it does:**
Deletes all files under `manifests/<image>/<tag>/`. Blobs are not touched — they remain in `blobs/sha256/` and may be referenced by other tags. Run `s3lo clean --blobs` after deleting tags to reclaim blob storage.

**Examples:**
```bash
# Delete a tag
s3lo delete s3://my-bucket/myapp:v1.0

# Delete a tag then reclaim blob storage
s3lo delete s3://my-bucket/myapp:v1.0
s3lo clean s3://my-bucket/ --blobs --confirm
```

**Notes:**
- Deleting a tag that does not exist returns an error.
- Deleting a tag does not affect running containers — the blobs remain available until gc removes them.

---

### clean

Prune old image tags according to lifecycle rules and garbage collect unreferenced blobs.

```
s3lo clean <s3-bucket-ref> [flags]
```

**Flags:**
- `--confirm` - actually delete (default is dry-run)
- `--tags` - only prune old tags, skip blob GC
- `--blobs` - only GC unreferenced blobs, skip tag pruning
- `--config <file>` - path to a local BucketConfig YAML file (optional; defaults to bucket's `s3lo.yaml`)

**What it does:**

*Tag pruning (default, skipped with `--blobs`):*
1. Reads lifecycle rules from `s3lo.yaml` in the bucket (or `--config` file).
2. For each image, evaluates all tags against the configured rules.
3. Tags that violate `keep_last`, `max_age`, or are not in `keep_tags` are candidates for deletion.
4. In dry-run mode, reports what would be deleted. With `--confirm`, deletes manifest files.

*Blob GC (default, skipped with `--tags`):*
1. Reads all `manifest.json` files in the bucket in parallel (20 concurrent workers).
2. Builds a set of all referenced blob digests.
3. Lists all blobs in `blobs/sha256/`.
4. Blobs not referenced by any manifest and older than 1 hour are candidates for deletion.
5. In dry-run mode, reports unreferenced blobs. With `--confirm`, deletes them.

**Examples:**
```bash
# Dry run — see what would be deleted (safe, no changes)
s3lo clean s3://my-bucket/

# Full cleanup — prune tags + GC blobs
s3lo clean s3://my-bucket/ --confirm

# Only prune old tags (don't touch blobs yet)
s3lo clean s3://my-bucket/ --tags --confirm

# Only GC unreferenced blobs (e.g. after manual deletes)
s3lo clean s3://my-bucket/ --blobs --confirm

# Use a local config file instead of the bucket's s3lo.yaml
s3lo clean s3://my-bucket/ --config override.yaml --confirm
```

**Output (dry run):**
```
Tags:  12 would be deleted (out of 47 evaluated)
Blobs: 3 unreferenced (112.40 MB would be freed)

Run with --confirm to apply changes.
```

**Output (confirmed):**
```
Tags:  12 deleted (out of 47 evaluated)
Blobs: 3 deleted (112.40 MB freed)
```

**Lifecycle rule fields:**
- `keep_last` - keep the N most recently pushed tags. 0 or omitted means no limit.
- `max_age` - delete tags older than this duration. Supports `Nd` (e.g. `7d`, `90d`) and Go duration strings (e.g. `168h`).
- `keep_tags` - tag names never deleted regardless of other rules.

When both `keep_last` and `max_age` are set, a tag is deleted if it violates either condition.

**Safety:**
- Default is always dry-run. No deletions without `--confirm`.
- The 1-hour blob grace period prevents race conditions with in-progress pushes.
- Configure lifecycle rules with `s3lo config set` before running.

**Scheduling:**
`clean` does not run automatically. Schedule it via CI, cron, or Lambda to enforce lifecycle rules. See [CI Integration](#ci-integration) for examples.

---

### stats

Show storage usage, deduplication savings, and cost estimate for a bucket.

```
s3lo stats <s3-bucket-ref>
```

**What it does:**
1. Scans all manifests under `manifests/` to count images, tags, and sum logical blob bytes.
2. Lists all blobs under `blobs/sha256/` to get actual stored bytes and storage class breakdown.
3. Calculates deduplication savings: logical bytes minus actual stored bytes.
4. Estimates monthly cost vs ECR equivalent.

**Examples:**
```bash
s3lo stats s3://my-bucket/
```

**Output:**
```
Bucket: s3://my-bucket/

Images:       12
Tags:         47
Unique blobs: 89
Total size:   2.4 GB

Dedup savings: 1.8 GB (43% saved)

Storage class breakdown:
  INTELLIGENT_TIERING:           2.2 GB (91%)
  STANDARD:                      0.2 GB (9%)

Estimated monthly cost: $0.06
vs ECR equivalent:      $0.24 (4.3x cheaper)
```

---

### config

Manage per-bucket s3lo configuration stored at `s3://bucket/s3lo.yaml`.

```
s3lo config set <s3-ref> <key>=<value> [<key>=<value> ...]
s3lo config get <s3-ref>
s3lo config remove <s3-ref> [key]
s3lo config recommend <s3-bucket-ref>
```

Configuration is stored per-image. Use `s3://bucket/` to set bucket-wide defaults, or `s3://bucket/myapp` to set overrides for a specific image. Glob patterns (e.g. `s3://bucket/dev/*`) are supported.

**Available keys:**

| Key | Values | Description |
|-----|--------|-------------|
| `immutable` | `true` / `false` | Reject pushes that would overwrite an existing tag |
| `lifecycle.keep_last` | integer | Keep the N most recently pushed tags |
| `lifecycle.max_age` | duration (e.g. `30d`, `168h`) | Delete tags older than this |
| `lifecycle.keep_tags` | comma-separated tags | Tags never deleted by lifecycle |

---

#### config set

```bash
# Set bucket-wide defaults
s3lo config set s3://my-bucket/ immutable=false lifecycle.keep_last=10 lifecycle.max_age=90d

# Set per-image overrides
s3lo config set s3://my-bucket/myapp immutable=true lifecycle.keep_last=5 lifecycle.keep_tags=stable,latest

# Set for a glob pattern (all dev/* images)
s3lo config set s3://my-bucket/dev/* lifecycle.max_age=7d lifecycle.keep_tags=latest
```

#### config get

```bash
# Show all config (defaults + per-image overrides)
s3lo config get s3://my-bucket/

# Show effective config for a specific image (with source labels)
s3lo config get s3://my-bucket/myapp
```

**Output (`config get s3://my-bucket/`):**
```
Bucket: s3://my-bucket/

Default:
  immutable:                     false
  lifecycle.keep_last:           10
  lifecycle.max_age:             90d

Images:
  myapp
    immutable:                   true
    lifecycle.keep_last:         5
    lifecycle.keep_tags:         stable, latest
  dev/*
    lifecycle.max_age:           7d
    lifecycle.keep_tags:         latest
```

**Output (`config get s3://my-bucket/myapp`):**
```
Image: myapp (s3://my-bucket/)

  immutable:                     true   [image]
  lifecycle.keep_last:           5      [image]
  lifecycle.max_age:             90d    [default]
  lifecycle.keep_tags:           stable, latest  [image]
```

#### config remove

```bash
# Remove all overrides for an image (reverts to defaults)
s3lo config remove s3://my-bucket/myapp

# Remove a specific override
s3lo config remove s3://my-bucket/myapp immutable
s3lo config remove s3://my-bucket/myapp lifecycle
```

#### config recommend

Analyzes the actual bucket state and provides data-driven recommendations.

```bash
s3lo config recommend s3://my-bucket/
```

**What it checks:**
- Versioning status (should be disabled for s3lo buckets)
- Existing S3 lifecycle rules
- Incomplete multipart uploads
- Whether lifecycle rules are configured in `s3lo.yaml`

**Output:**
```
Analysis for s3://my-bucket/:

  [good] Versioning: disabled
  [bad]  S3 lifecycle rules: none
  [good] Incomplete multipart uploads: none
  [bad]  s3lo lifecycle config: not configured

Recommendations:

1. Add S3 lifecycle rule to abort incomplete multipart uploads
   ...

2. Configure lifecycle rules to automatically clean old tags
   s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d
   s3lo clean s3://my-bucket/ --confirm
```

**Config storage:**
The config is a YAML file stored at `s3://bucket/s3lo.yaml`. It is read on every `push` call and by `clean`. Requires `s3:GetObject` and `s3:PutObject` permissions.

**Pattern matching:**
When multiple patterns match an image, the most specific wins: exact matches take precedence over globs; longer patterns take precedence over shorter ones.

---

### version

Print the s3lo version and build commit.

```
s3lo version
```

**Output:**
```
s3lo v1.3.0 (abc1234)
```

---

## Authentication

s3lo uses the standard AWS SDK credential chain. It does not have its own authentication mechanism. Credentials are resolved in this order:

1. `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` environment variables
2. `AWS_PROFILE` environment variable (selects a named profile)
3. `~/.aws/credentials` and `~/.aws/config` (default profile)
4. IAM instance profile (EC2, ECS task role, etc.)
5. EKS Pod Identity / IRSA
6. OIDC web identity token (CI/CD)

**Region:**
s3lo auto-detects the bucket region using `s3:GetBucketLocation`. You do not need to specify a region.

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

### Per-command breakdown

| Command | Required S3 Actions |
|---------|---------------------|
| push | GetObject, PutObject, HeadObject, ListBucket, GetBucketLocation |
| pull | GetObject, HeadObject, ListBucket, GetBucketLocation |
| copy (S3 src) | GetObject, PutObject, HeadObject, ListBucket, GetBucketLocation |
| copy (ECR src) | Same as push, plus `ecr:GetAuthorizationToken` |
| list | ListBucket, GetBucketLocation |
| inspect | GetObject, GetBucketLocation |
| delete | DeleteObject, ListBucket, GetBucketLocation |
| clean | GetObject, DeleteObject, ListBucket, GetBucketLocation |
| stats | GetObject, ListBucket, GetBucketLocation |
| config set | GetObject, PutObject, GetBucketLocation |
| config get | GetObject, GetBucketLocation |
| config recommend | GetBucketLocation, GetBucketVersioning, GetBucketLifecycleConfiguration, ListMultipartUploads |

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
| S3 Intelligent-Tiering | $0.023 (frequent) to $0.004 (deep archive) | Blobs — automatic tiering |
| ECR | $0.10 | (comparison baseline) |

---

## Deduplication

s3lo deduplicates at the blob level using SHA256 content addressing.

**How it works:**
1. Before uploading a blob, s3lo checks if the key `blobs/sha256/<digest>` already exists in S3.
2. If it exists, the upload is skipped entirely.
3. If it does not exist, it is uploaded.

This means:
- Two different image tags with the same base layer store that layer once.
- Rebuilding an image with only code changes does not re-upload unchanged layers.
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
    "github.com/OuFinx/s3lo/pkg/image" // Push, pull, list, inspect, delete, gc, stats, copy
)
```

All functions accept `context.Context` as the first argument for cancellation and deadline support.

### Push an image

```go
err := image.Push(ctx, "myapp:v1.0", "s3://my-bucket/myapp:v1.0", image.PushOptions{})
```

### Pull an image

```go
err := image.Pull(ctx, "s3://my-bucket/myapp:v1.0", "myapp:v1.0", image.PullOptions{})
```

### List images

```go
images, err := image.List(ctx, "s3://my-bucket/")
```

### Inspect metadata

```go
meta, err := image.Inspect(ctx, "s3://my-bucket/myapp:v1.0")
fmt.Printf("arch: %s, layers: %d\n", meta.Architecture, len(meta.Layers))
```

### Garbage collect

```go
result, err := image.GC(ctx, "s3://my-bucket/", false) // false = not dry run
fmt.Printf("deleted %d blobs, freed %d bytes\n", result.Deleted, result.FreedBytes)
```

### Apply lifecycle rules

```go
cfg, err := image.GetBucketConfig(ctx, client, "my-bucket")
result, err := image.ApplyLifecycle(ctx, "s3://my-bucket/", cfg, false)
fmt.Printf("deleted %d tags out of %d evaluated\n", result.Deleted, result.Evaluated)
```

### Parse a reference

```go
r, err := ref.Parse("s3://my-bucket/myapp:v1.0")
fmt.Println(r.Bucket) // "my-bucket"
fmt.Println(r.Image)  // "myapp"
fmt.Println(r.Tag)    // "v1.0"
```

---

## CI Integration

### GitHub Actions — push on commit

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

### GitHub Actions — scheduled cleanup

```yaml
name: Cleanup

on:
  schedule:
    - cron: '0 2 * * *'  # nightly at 2 AM

jobs:
  clean:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read

    steps:
      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789:role/ci-s3lo-role
          aws-region: us-east-1

      - name: Install s3lo
        run: |
          curl -Lo s3lo.tar.gz https://github.com/OuFinx/s3lo/releases/latest/download/s3lo_linux_amd64.tar.gz
          tar xzf s3lo.tar.gz && chmod +x s3lo && sudo mv s3lo /usr/local/bin/

      - name: Clean old tags and unreferenced blobs
        run: s3lo clean s3://my-bucket/ --confirm
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

---

## FAQ

**Q: Can I use s3lo without Docker?**

Not currently. s3lo uses the Docker daemon to export images (`docker save`) and to import them (`docker load`). Docker must be running locally.

**Q: Does s3lo support multi-architecture images?**

Yes, since v1.3.0. s3lo stores OCI Image Indexes natively. `copy` copies all platforms by default, preserving the full index at the destination. `pull` auto-detects the host platform automatically — no flags needed.

**Q: What happens if a push is interrupted?**

Blobs uploaded before the interruption remain in S3. Re-running the push will skip those blobs and continue. The manifest files are uploaded last — if the push is interrupted before the manifest is written, the image will not appear in `s3lo list` but the orphaned blobs will be cleaned up by `s3lo clean --blobs`.

**Q: Can two pushes run in parallel to the same tag?**

This is a race condition. The last manifest to be written wins. Blob uploads are safe to parallelize (same content, same key), but two simultaneous pushes of different images could result in an inconsistent manifest. Serialize pushes to the same tag.

**Q: How do I delete all tags for an image?**

```bash
s3lo list s3://my-bucket/ | grep "myapp:" | while read ref; do
  s3lo delete "s3://my-bucket/$ref"
done
s3lo clean s3://my-bucket/ --blobs --confirm
```

**Q: Is there a way to copy an image between buckets?**

Yes, use `s3lo copy`:
```bash
s3lo copy s3://source-bucket/myapp:v1.0 s3://dest-bucket/myapp:v1.0
```

**Q: Why does pull show "image imported into Docker" but `docker images` shows nothing?**

The import uses `docker load` which should appear immediately. Check if Docker is running and if there are errors in the output. Also verify the image was pushed with the correct platform (`linux/amd64` for non-ARM hosts).

**Q: Does s3lo work with S3-compatible storage (MinIO, Backblaze, etc.)?**

Not officially yet. The AWS SDK's endpoint override (`AWS_ENDPOINT_URL`) may work for testing, but this is unsupported.

**Q: How do I see s3lo version?**

```bash
s3lo version
```

**Q: Can I use s3lo without AWS CLI installed?**

Yes. s3lo uses the AWS SDK directly and does not require the AWS CLI. You only need valid AWS credentials (environment variables, `~/.aws/credentials`, or an IAM role).

**Q: What is the maximum image size?**

There is no hard limit in s3lo. S3 supports objects up to 5 TB. s3lo uploads each blob as a single S3 PutObject, which supports up to 5 GB per blob. For images with blobs larger than 5 GB, multipart upload would be needed (not currently implemented).

**Q: How do I migrate from ECR to S3?**

Use `s3lo copy` — it authenticates automatically using your AWS credentials:
```bash
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

**Q: What does the 1-hour gc grace period protect against?**

If a push is in progress and `clean --blobs` runs at the same time, blobs that have been uploaded but whose manifest has not been written yet would be incorrectly identified as unreferenced. The grace period ensures blobs uploaded in the last hour are never deleted, eliminating this race condition.

**Q: How do lifecycle rules work with per-image overrides?**

Bucket-wide defaults apply to all images. Per-image overrides (set via `s3lo config set s3://bucket/myapp ...`) take precedence over defaults. When multiple glob patterns match an image, the most specific wins (exact matches before globs, longer patterns before shorter).
