# clean

Prune old image tags according to lifecycle rules and garbage collect unreferenced blobs.

```
s3lo clean <s3-bucket-ref> [flags]
```

## Flags

| Flag | Description |
|------|-------------|
| `--confirm` | Apply deletions. Without this flag, the command is a dry run (safe, no changes). |
| `--tags` | Only prune old tags, skip blob GC. |
| `--blobs` | Only GC unreferenced blobs, skip tag pruning. |
| `--config <file>` | Use a local config YAML file instead of the bucket's `s3lo.yaml`. |

## How it works

`clean` does two things by default, both controlled by lifecycle rules in `s3lo.yaml`:

**1. Tag pruning**

Reads lifecycle rules from `s3lo.yaml`. For each image, evaluates all tags against:

- `lifecycle.keep_last` — keep the N most recently pushed tags
- `lifecycle.max_age` — delete tags older than this duration
- `lifecycle.keep_tags` — tags that are never deleted regardless of other rules

Tags violating any rule are candidates for deletion.

**2. Blob garbage collection**

Reads all manifests in the bucket, builds a set of referenced blob digests, then deletes any blob in `blobs/sha256/` that:

- Is not referenced by any manifest, **and**
- Was uploaded more than 1 hour ago (grace period to protect in-progress pushes)

## Examples

```bash
# Dry run — see what would be deleted (no changes made)
s3lo clean s3://my-bucket/

# Full cleanup: prune tags + GC blobs
s3lo clean s3://my-bucket/ --confirm

# Only prune old tags (keep blobs for now)
s3lo clean s3://my-bucket/ --tags --confirm

# Only GC unreferenced blobs (e.g. after manual deletes)
s3lo clean s3://my-bucket/ --blobs --confirm
```

## Output

=== "Dry run"

    ```
    Tags:  12 would be deleted (out of 47 evaluated)
    Blobs: 3 unreferenced (112.40 MB would be freed)

    Run with --confirm to apply changes.
    ```

=== "Confirmed"

    ```
    Tags:  12 deleted (out of 47 evaluated)
    Blobs: 3 deleted (112.40 MB freed)
    ```

## Setting up lifecycle rules

Before running `clean`, configure lifecycle rules with [`s3lo config set`](config.md):

```bash
# Keep last 10 tags, delete anything older than 90 days
s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d

# Override for a specific image: keep last 5 and protect stable/latest tags
s3lo config set s3://my-bucket/myapp lifecycle.keep_last=5 lifecycle.keep_tags=stable,latest

# Aggressive cleanup for dev/* images: keep only last 3 tags, max 7 days
s3lo config set "s3://my-bucket/dev/*" lifecycle.keep_last=3 lifecycle.max_age=7d
```

!!! tip "Schedule with GitHub Actions"
    `clean` doesn't run automatically. Add a scheduled workflow to run it nightly:
    ```yaml
    on:
      schedule:
        - cron: '0 2 * * *'
    ```
    See the [GitHub Actions guide](../guides/github-actions.md#scheduled-cleanup) for the full workflow.

!!! info "Safety"
    - Default is always dry run. Nothing is deleted without `--confirm`.
    - Blobs uploaded in the last hour are never deleted, protecting in-progress pushes.
    - `keep_tags` are never deleted regardless of age or count.
