# Lifecycle Rules

Lifecycle rules automatically clean old image tags, keeping storage lean and costs low.

## How lifecycle rules work

Rules are stored in `s3lo.yaml` at the bucket root and evaluated by `s3lo clean`. You can set bucket-wide defaults and per-image overrides.

| Rule | Description |
|------|-------------|
| `lifecycle.keep_last` | Keep the N most recently pushed tags. Delete the rest. |
| `lifecycle.max_age` | Delete tags older than this duration. Supports `Nd` (e.g. `7d`, `90d`) and Go duration strings (`168h`). |
| `lifecycle.keep_tags` | Named tags that are **never** deleted regardless of other rules. |

When both `keep_last` and `max_age` are set, a tag is deleted if it violates **either** rule.

## Recommended setup

### Step 1: Set rules

```bash
# Bucket-wide defaults: keep last 10 tags, max 90 days
s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d

# Production images: keep last 5, protect stable/latest
s3lo config set s3://my-bucket/myapp immutable=true lifecycle.keep_last=5 lifecycle.keep_tags=stable,latest

# Dev images: aggressive cleanup, 7 days max
s3lo config set "s3://my-bucket/dev/*" lifecycle.keep_last=3 lifecycle.max_age=7d
```

### Step 2: Dry run

Always dry run first to see what would be deleted:

```bash
s3lo clean s3://my-bucket/
```

Output:
```
Tags:  8 would be deleted (out of 23 evaluated)
Blobs: 2 unreferenced (45.20 MB would be freed)

Run with --confirm to apply changes.
```

### Step 3: Apply

```bash
s3lo clean s3://my-bucket/ --confirm
```

### Step 4: Schedule with GitHub Actions

```yaml
name: Cleanup

on:
  schedule:
    - cron: '0 2 * * *'  # nightly at 2 AM UTC

jobs:
  clean:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/ci-s3lo-role
          aws-region: us-east-1

      - run: s3lo clean s3://my-bucket/ --confirm
```

## Common patterns

=== "Keep last N"

    ```bash
    # Keep last 10 tags per image
    s3lo config set s3://my-bucket/ lifecycle.keep_last=10
    ```
    Good for: images with frequent releases where you want a rolling window.

=== "Max age"

    ```bash
    # Delete anything older than 30 days
    s3lo config set s3://my-bucket/ lifecycle.max_age=30d
    ```
    Good for: dev/staging buckets where old images are never needed.

=== "Protect named tags"

    ```bash
    # Never delete 'latest' or 'stable', even if older than max_age
    s3lo config set s3://my-bucket/myapp lifecycle.keep_tags=latest,stable lifecycle.max_age=30d
    ```
    Good for: production images that have a "stable" pointer tag.

=== "Combined (recommended)"

    ```bash
    s3lo config set s3://my-bucket/ \
      lifecycle.keep_last=10 \
      lifecycle.max_age=90d \
      lifecycle.keep_tags=latest,stable
    ```
    Keeps last 10 tags OR anything under 90 days AND always keeps `latest` and `stable`.

## Blob GC

`clean` also runs blob garbage collection by default. After tag pruning, it checks which blobs are still referenced by any remaining tag, and deletes the rest (with a 1-hour grace period for in-progress pushes).

Run blob GC separately if needed:

```bash
# GC blobs without touching tags
s3lo clean s3://my-bucket/ --blobs --confirm
```

## Viewing the config

```bash
s3lo config get s3://my-bucket/
```

Use `config recommend` to get analysis and suggestions based on actual bucket state:

```bash
s3lo config recommend s3://my-bucket/
```
