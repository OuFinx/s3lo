# config

Manage per-image and bucket-wide configuration stored in `s3lo.yaml`.

```
s3lo config set <ref> <key>=<value> [<key>=<value> ...]
s3lo config get <ref>
s3lo config remove <ref> [key]
s3lo config recommend <bucket-ref>
```

Both `s3://` and `local://` references are supported.

## Configuration keys

| Key | Values | Description |
|-----|--------|-------------|
| `immutable` | `true` / `false` | Reject pushes to existing tags |
| `lifecycle.keep_last` | integer | Keep N most recently pushed tags |
| `lifecycle.max_age` | duration (`30d`, `168h`) | Delete tags older than this |
| `lifecycle.keep_tags` | comma-separated tags | Tags never deleted by lifecycle |

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

## config remove

```bash
# Remove all overrides for an image (reverts to bucket defaults)
s3lo config remove s3://my-bucket/myapp

# Remove a specific key
s3lo config remove s3://my-bucket/myapp immutable

# Remove all lifecycle overrides
s3lo config remove s3://my-bucket/myapp lifecycle
```

## config validate

Run all policies defined in `s3lo.yaml` against a stored image tag.

```bash
s3lo config validate s3://my-bucket/myapp:v1.0
```

Exit codes:

- `0` — all policies passed
- `1` — one or more policies failed

### Policy configuration

Define policies in `s3lo.yaml` under the `policies` key:

```yaml
policies:
  - name: no-critical-vulns
    check: scan
    max_severity: HIGH
  - name: max-age
    check: age
    max_days: 90
  - name: require-signature
    check: signed
    key_ref: cosign.pub
  - name: max-size
    check: size
    max_bytes: 1073741824
```

### Policy checks

| Check | Description | Parameters |
|-------|-------------|------------|
| `scan` | Run Trivy; fail if vulnerabilities at or above severity | `max_severity`: LOW, MEDIUM, HIGH, CRITICAL |
| `age` | Fail if image is older than N days | `max_days` |
| `signed` | Verify the current manifest with a trusted key | `key_ref`: public key or KMS/Vault verifier reference |
| `size` | Fail if total image size exceeds N bytes | `max_bytes` |

### Example output

```
✓ no-critical-vulns    passed
✗ max-age              FAILED (image is 127 days old, limit is 90)
✓ require-signature    passed (verified by 3b780f64bd6940e1)

1 policy failed.
```

### CI integration

```yaml
# GitHub Actions
- name: Validate image policies
  run: s3lo config validate s3://my-bucket/myapp:${{ github.sha }}
```

The non-zero exit code causes the CI step to fail automatically when any policy is violated.

---

## config recommend

Analyzes the bucket and suggests configuration changes.

```bash
s3lo config recommend s3://my-bucket/
```

Output:
```
Analysis for s3://my-bucket/:

  [good] Versioning: disabled
  [bad]  S3 lifecycle rules: none
  [good] Incomplete multipart uploads: none
  [bad]  s3lo lifecycle config: not configured

Recommendations:

1. Add an S3 lifecycle rule to abort incomplete multipart uploads after 1 day.
   This prevents orphaned uploads from accumulating storage costs.

2. Configure lifecycle rules to automatically clean old tags:
   s3lo config set s3://my-bucket/ lifecycle.keep_last=10 lifecycle.max_age=90d
   s3lo clean s3://my-bucket/ --confirm
```
