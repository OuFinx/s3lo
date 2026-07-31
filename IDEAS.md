## Near-Term Features

### 1. `s3lo doctor` — Bucket health check

Validate bucket integrity: find orphaned blobs, corrupt or missing manifests, layout consistency issues, config drift. Images with missing blobs should be flagged for removal since they can't be repaired.

Example usage:
```
s3lo doctor s3://my-bucket/

Checking layout structure...     OK
Checking manifest integrity...   2 issues
  ✗ myapp:v0.3 — manifest references blob sha256:abc123 which does not exist (corrupted, cannot be repaired)
  ✗ webapp:old — manifest references blob sha256:def456 which does not exist (corrupted, cannot be repaired)
Checking for orphaned blobs...   3 orphaned blobs (28.4 MB)
Checking config consistency...   OK

Corrupted images found. Run:
  s3lo delete s3://my-bucket/myapp:v0.3
  s3lo delete s3://my-bucket/webapp:old
  s3lo clean s3://my-bucket/ --blobs --confirm
```

### 2. Deterministic progress bars with total size (issue #44)

Push and pull already know the total blob sizes before starting. Change the indeterminate spinner to a real percentage bar showing `downloading 1.2 GB / 3.4 GB`. Small effort, big UX impact.

### 3. `s3lo init` — Bucket initialization with local mode

Initialize a bucket for s3lo use: create the layout structure, apply recommended Intelligent-Tiering settings, set up default `s3lo.yaml` config. Includes `--local` flag to create a local filesystem-based S3 storage so users can try s3lo without an AWS account.

Example usage:
```
# Cloud mode
s3lo init s3://my-new-bucket/

✓ Verified bucket exists and is accessible
✓ Created s3lo.yaml with recommended defaults
✓ Intelligent-Tiering configuration detected

Your bucket is ready. Try:
  s3lo push myapp:latest s3://my-new-bucket/myapp:latest

# Local mode (no AWS needed)
s3lo init --local

✓ Created local storage at ~/.s3lo/local/
✓ Created s3lo.yaml with defaults

Your local storage is ready. Try:
  s3lo push myapp:latest local://myapp:latest
```

### 4. Consistent `--output json|yaml|table` flag across all commands

Essential for scripting, automation, and building on top of s3lo. Adding structured output makes s3lo composable with `jq`, CI pipelines, and other tools.

Affected commands: `list`, `inspect`, `stats`, `config get`, `doctor`.

Example usage:
```
s3lo list s3://my-bucket/ --output json | jq '.[].tag'
s3lo stats s3://my-bucket/ --output json | jq '.dedup_savings_bytes'
s3lo inspect s3://my-bucket/myapp:v1.0 --output yaml
```

### 5. `s3lo history` — Tag push history

Show the push/tag history for an image: timestamp, size, digest. Could leverage a lightweight event log stored as a JSON file alongside manifests, or S3 object versioning metadata.

Example usage:
```
s3lo history s3://my-bucket/myapp:latest

TAG       PUSHED               SIZE      DIGEST
latest    2026-04-11 14:30:02  142.3 MB  sha256:a1b2c3d4...
latest    2026-04-10 09:15:44  141.8 MB  sha256:e5f6g7h8...
latest    2026-04-08 17:22:11  139.2 MB  sha256:i9j0k1l2...
```

### 6. Multipart upload for large blobs (>5 GB)

Currently limited to 5 GB per blob due to single-part PutObject. Supporting S3 multipart upload removes this ceiling and makes s3lo viable for ML model containers, data science images, and other large images.

### 7. Enhanced cost comparison in `stats`

Enhance `stats` to project monthly costs vs ECR pricing, show savings over time, and estimate the impact of dedup.

Example output:
```
s3lo stats s3://my-bucket/

Storage: 12.4 GB across 847 blobs
Dedup savings: 8.2 GB (39.8% deduction)
Effective storage: 12.4 GB (20.6 GB without dedup)

Estimated monthly cost:
  S3 (current):     $0.29/month
  S3 (no dedup):    $0.47/month
  ECR equivalent:   $2.06/month
  Savings vs ECR:   $1.77/month (86%)
```

### 8. Structured logging + `--verbose` / `--debug` (issue #43)

Replace `fmt.Printf` with `slog` or similar structured logger. Add `--verbose` for detailed operation output and `--debug` for full wire-level tracing (S3 API calls, HTTP requests). Essential for troubleshooting in production and CI environments.

### 9. Config validation / policy checks

Add policy rules to `s3lo.yaml` and a `s3lo config validate` subcommand to enforce them. Useful for compliance and governance.

Example config:
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
```

Example usage:
```
s3lo config validate s3://my-bucket/myapp:v1.0

✓ no-critical-vulns    passed
✗ max-age              FAILED (image is 127 days old, limit is 90)
✓ require-signature    passed
```

---

## Tools

### 10. `s3lo sbom` — Software Bill of Materials

Generate SBOM from S3-stored images in SPDX or CycloneDX format. Pairs with `scan` and the planned `sign`/`verify`. SBOM requirements are becoming mandatory in regulated industries (US Executive Order 14028, EU CRA).

Example usage:
```
s3lo sbom s3://my-bucket/myapp:v1.0 --format spdx-json > myapp-v1.0.sbom.json
s3lo sbom s3://my-bucket/myapp:v1.0 --format cyclonedx
```

---

## Long-Term Vision

### 11. `s3lo serve` — OCI Distribution proxy

A lightweight HTTP proxy that speaks the OCI Distribution Spec and serves images from S3. This lets `docker pull localhost:5000/myapp:v1` work against S3-stored images. Removes the "can't use docker pull" limitation.

Example usage:
```
s3lo serve s3://my-bucket/ --port 5000

# Then in another terminal:
docker pull localhost:5000/myapp:v1.0
```

### 12. Interactive TUI (terminal UI)

A terminal UI using bubbletea/lipgloss for browsing images, viewing stats, managing lifecycle, and triggering operations. Makes the tool feel premium and discoverable for new users.

### 13. Terraform provider

Manage s3lo bucket config and image lifecycle as Terraform resources. Natural fit for infrastructure-as-code teams.

Example:
```hcl
resource "s3lo_bucket" "images" {
  bucket = "my-images"
  config {
    immutable_tags = ["prod-*"]
    keep_last      = 10
    max_age_days   = 90
  }
}
```
