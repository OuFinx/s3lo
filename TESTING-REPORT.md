# s3lo Testing Report

**Date:** 2026-04-12
**Version:** `s3lo v1.7.0-8-g8cdb8a2 (8cdb8a2)` (branch `v1.8.0`)
**S3 Bucket:** `s3://test-ecr-home/` (eu-central-1, profile `itshomedude`)
**Local Storage:** `local://./test-local-s3/`
**Test Image:** `alpine:latest` (single-arch, ~8.6 MB)

---

## Summary

| Command | S3 | Local | Verdict |
|---------|:--:|:-----:|---------|
| `init` | OK | OK | Pass |
| `push` | OK | OK | Pass |
| `pull` | OK | OK | Pass |
| `copy` | OK | OK | Pass (with findings) |
| `list` | OK | OK | Pass |
| `inspect` | OK | OK | Pass (with findings) |
| `history` | OK | OK | Pass (with findings) |
| `stats` | OK | OK | Pass (with findings) |
| `config set/get/remove` | OK | OK | Pass |
| `config recommend` | OK | N/A | Pass (expected — not supported for local) |
| `scan` | OK | OK | Pass |
| `doctor` | OK | OK | Pass |
| `delete` | OK | OK | Pass |
| `clean` | OK | OK | Pass (with findings) |
| Error handling | OK | OK | Pass |

**Overall: all commands work for both S3 and local storage. No crashes or data corruption. Several UX/logic findings below.**

---

## Findings

### ISSUE-1: `copy` does not write `history.json` — copied images invisible to `history`

**Severity:** Medium
**Affects:** `copy`, `history`

When using `s3lo copy alpine:latest local://./test-local-s3/alpine-copy:latest`, the copied image appears in `list` but not in `history`:

```
$ s3lo history local://./test-local-s3/
REPOSITORY            TAGS   LAST PUSHED           TOTAL SIZE
------------------------------------------------------------------
alpine                2      2026-04-12 15:15:33   17.1 MB
localcopy             1      ...

$ s3lo history local://./test-local-s3/alpine-copy
No push history recorded for alpine-copy.
```

**Root cause:** `copy` doesn't write `history.json` entries. Only `push` records history.

**Fix:** Write a `history.json` entry during `copy` operations, similar to what `push` does.

---

### ISSUE-2: `inspect` shows `unknown/unknown` platform for OCI attestation manifests

**Severity:** Low (cosmetic)
**Affects:** `inspect`

Multi-arch images from Docker Hub include attestation manifests alongside platform manifests. These show as `Platform: unknown/unknown` in inspect output:

```
Platform: linux/amd64
Digest:   sha256:59855d3dceb3...
Layers:   1
Size:     3.68 MB

Platform: unknown/unknown
Digest:   sha256:fe2385f27693...
Layers:   2
Size:     0.08 MB
```

This is confusing — users may wonder what `unknown/unknown` means.

**Fix options:**
- A) Filter out attestation manifests from the default display (they have `mediaType` of `application/vnd.oci.image.manifest.v1+json` with an `unknown` platform and typically contain a `vnd.in-toto+json` layer)
- B) Label them as `(attestation)` instead of `unknown/unknown`

---

### ISSUE-3: `doctor` and `clean` disagree on orphaned blobs (1-hour grace period)

**Severity:** Low (confusing)
**Affects:** `doctor`, `clean`

`doctor` reports orphaned blobs but `clean` reports 0 unreferenced:

```
$ s3lo doctor local://./test-local-s3/
Checking for orphaned blobs...  54 blobs (29.0 MB)
  s3lo clean local://./test-local-s3/ --blobs --confirm

$ s3lo clean local://./test-local-s3/
Blobs: 0 unreferenced (0.00 MB would be freed)
```

**Root cause:** `clean` applies a 1-hour grace period (won't delete recently-uploaded blobs), but `doctor` does not apply this grace period. For freshly-uploaded test data, they always disagree.

**Fix options:**
- A) Make `doctor` apply the same grace period and note it in output
- B) Make `doctor` report the count but note "(within grace period — clean will skip these until they age past 1 hour)"
- C) Accept the difference but document it

---

### ISSUE-4: `inspect --output yaml` produces verbose output with empty fields

**Severity:** Low (cosmetic)
**Affects:** `inspect`

YAML output includes many empty/zero/null fields from Go struct serialization:

```yaml
config:
  artifacttype: ""
  urls: []
  annotations: {}
  data: []
  platform: null
layers:
  - urls: []
    annotations: {}
    data: []
    platform: null
    artifacttype: ""
subject: null
annotations: {}
```

**Fix:** Use `omitempty` YAML tags on the OCI manifest struct fields so empty values are omitted.

---

### ISSUE-5: `stats` shows `$0.00` savings when amounts are too small to display

**Severity:** Low (cosmetic)
**Affects:** `stats`

For small test datasets:
```
Estimated monthly cost:
  S3 (current):              $0.00/month
  ECR equivalent:            $0.00/month
  Savings vs ECR:            $0.00/month (75% cheaper)
```

Showing "$0.00 savings (75% cheaper)" is technically correct but reads oddly.

**Fix options:**
- A) Show more decimal places for sub-penny costs (e.g. `$0.0008/month`)
- B) Show `< $0.01/month` instead of `$0.00/month`

---

### ISSUE-6: `--verbose` flag has no visible effect on some commands

**Severity:** Low
**Affects:** `list`, possibly others

Running `s3lo --verbose list local://./test-local-s3/` produces identical output to running without `--verbose`. No additional debug information is shown.

**Fix:** Either add debug logging (API calls, timing, blob counts) when `--verbose` is set, or document which commands support it.

---

### ISSUE-7: No region fallback or friendly error when AWS profile has no region

**Severity:** Medium
**Affects:** All S3 commands

If `AWS_PROFILE` is set but the profile has no `region` configured, the error is:

```
ERROR get bucket location for my-bucket: operation error S3: GetBucketLocation,
resolve auth scheme: resolve endpoint: endpoint rule error, Invalid region:
region was not a valid DNS name.
```

This is a raw AWS SDK error. Users won't know they need to set a region.

**Fix options:**
- A) Detect empty region in `NewClient` and return a friendly error: `"AWS region not configured. Set AWS_REGION or add region to your AWS profile."`
- B) Default to `us-east-1` for the initial `GetBucketLocation` call (it's a global operation)

---

### ISSUE-8: `pull` from nonexistent local directory gives cryptic error

**Severity:** Low
**Affects:** `pull`

```
$ s3lo pull local://./nonexistent-dir/app:v1
ERROR import into Docker: read manifest.json: open /var/folders/.../manifest.json: no such file or directory
```

The error references a temp directory path, not the user's `local://./nonexistent-dir/`.

**Fix:** Check if the local storage directory exists before attempting the pull and return a clear error like `"local storage directory not found: ./nonexistent-dir"`.

---

### ISSUE-9: `list` JSON/YAML and table output have different sort orders

**Severity:** Low (cosmetic)
**Affects:** `list`

Table output:
```
s3-to-local:v1
alpine:latest
alpine:v3.21
alpine-copy:latest
```

JSON output groups by image name alphabetically:
```json
[{"name":"alpine-copy","tags":["latest"]},{"name":"s3-to-local","tags":["v1"]},{"name":"alpine","tags":["latest","v3.21"]}]
```

YAML output uses yet another order. The inconsistency isn't harmful but is mildly surprising.

**Fix:** Use consistent alphabetical sorting across all output formats.

---

## Passed Tests (no issues)

### `init`
- `s3lo init --local ./test-local-s3` — creates directory, writes `s3lo.yaml` with defaults ✓
- Running twice — detects existing `s3lo.yaml`, creates dir idempotently ✓
- `s3lo init s3://test-ecr-home/` — verifies bucket access, checks Intelligent-Tiering ✓

### `push`
- Local and S3 both work ✓
- Deduplication works (second push skips blobs) ✓
- Missing tag rejected with clear error ✓
- `--force` overrides immutability ✓
- Push to immutable image without `--force` blocked with helpful error ✓

### `pull`
- Local and S3 both work ✓
- Custom local tag (`mytest:pulled`) works ✓
- `--platform` flag works ✓
- Missing tag rejected ✓

### `copy`
- Registry → local ✓ (multi-arch, 16 platforms)
- Registry → S3 ✓ (dedup correctly skips existing blobs)
- S3 → local ✓
- Local → S3 ✓
- Local → local ✓
- Missing tag rejected ✓

### `config`
- `config set` / `config get` / `config remove` — all work for local and S3 ✓
- Per-image config with inheritance display ✓
- `config recommend` — properly says "not supported for local" ✓
- `config recommend` for S3 — gives actionable recommendations ✓

### `scan`
- Works for both local and S3 ✓
- `--severity` filter works ✓
- `--format json` produces valid JSON (Trivy output goes to stderr separately) ✓

### `doctor`
- Works for both local and S3 ✓
- Correctly identifies orphaned blobs from multi-arch copy ✓
- Suggests correct remediation command with proper scheme prefix ✓

### `delete`
- Works for both local and S3 ✓
- Deleting nonexistent tag gives clear error ✓
- Missing tag rejected ✓

### `clean`
- Dry run (default) works for both ✓
- `--tags`, `--blobs`, `--confirm` flags all work ✓

### Error handling
- No arguments → shows usage with correct arg count ✓
- Invalid scheme (`ftp://`) → clear "must start with s3:// or local://" ✓
- Missing tag → clear suggestion with example ✓
- Nonexistent Docker image → shows Docker daemon error ✓
- Red `ERROR` prefix in terminal ✓
- Usage shown after errors ✓

---

## Test Environment

- macOS (darwin/arm64)
- Go 1.26.2
- Docker Desktop running
- Trivy v0.69.0 (auto-installed)
- S3 bucket in eu-central-1
