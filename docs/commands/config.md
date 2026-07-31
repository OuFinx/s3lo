# config

Manage per-image and bucket-wide configuration stored in `s3lo.yaml`.

```
s3lo config set <ref> <key>=<value> [<key>=<value> ...]
s3lo config get <ref>
```

Both `s3://` and `local://` references are supported.

## Configuration keys

| Key | Values | Description |
|-----|--------|-------------|
| `immutable` | `true` / `false` | Reject pushes to existing tags |
| `lifecycle.keep_last` | integer | Keep N most recently pushed tags |
| `lifecycle.max_age` | duration (`30d`, `168h`) | Delete tags older than this |
| `lifecycle.keep_tags` | comma-separated tags | Tags never deleted by lifecycle |
| `chunked` | `true` / `false` (default `true`) | Store layers as shared chunks — bucket-wide only |

### `chunked`

On by default. Layers are stored as content-defined chunks shared across every
image in the bucket, so re-pushing an image after a small edit uploads only the
chunk that changed. See [Chunked storage](../concepts/chunking.md).

To store whole layers instead — the only reason being clients still on s3lo v1,
which cannot read a chunked layer:

```bash
s3lo config set s3://my-bucket/ chunked=false
```

It applies to the whole bucket and cannot be set per image: the chunk store is
shared, so a per-image switch would only make garbage collection harder to reason
about without making anything more useful. Switching it on or off is safe at any
time — images already in the bucket stay readable either way.

The first chunked push also records `chunk_format` in `s3lo.yaml`. That value is
managed by s3lo and should not be edited by hand.

## Scope

Config is per-image but inherits from bucket-wide defaults. Use:

- `s3://bucket/` or `local://./store/` — bucket-wide defaults (apply to all images)
- `s3://bucket/myapp` or `local://./store/myapp` — overrides for a specific image
- `s3://bucket/dev/*` — glob pattern (matches all images under `dev/`)

More specific patterns take precedence over less specific ones.

---

## config set

```bash
# Set bucket-wide defaults
s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d

# Per-image: immutable + custom lifecycle
s3lo config set s3://my-bucket/myapp immutable=true lifecycle.keep_last=5 lifecycle.keep_tags=stable,latest

# Glob pattern for dev images
s3lo config set "s3://my-bucket/dev/*" lifecycle.max_age=7d lifecycle.keep_last=3
```

## config get

```bash
# Show all config
s3lo config get s3://my-bucket/

# Show effective config for a specific image (shows where each value comes from)
s3lo config get s3://my-bucket/myapp
```

=== "Bucket output"

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

=== "Image output"

    ```
    Image: myapp (s3://my-bucket/)

      immutable:                     true   [image]
      lifecycle.keep_last:           5      [image]
      lifecycle.max_age:             90d    [default]
      lifecycle.keep_tags:           stable, latest  [image]
    ```

    The `[image]` / `[default]` labels show where each value originates.

## Unsetting a key

An empty value unsets the key, at bucket scope as well as image scope — every
key `config set` can write, it can also remove:

```bash
# Drop one image override (reverts to the bucket default)
s3lo config set s3://my-bucket/myapp immutable=

# Drop the whole lifecycle block for an image
s3lo config set s3://my-bucket/myapp lifecycle=

# Drop a single lifecycle field
s3lo config set s3://my-bucket/ lifecycle.max_age=

# Drop a bucket-wide setting
s3lo config set s3://my-bucket/ chunked=
```

An image whose last override is removed disappears from `s3lo.yaml` entirely.

!!! note "Replaces `config remove`"
    `s3lo config remove` was removed in v2.1 — it could not touch bucket-level
    keys, so anything set on the bucket was unremovable. `key=` covers both
    scopes.
