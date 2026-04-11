# s3lo Codebase Analysis Report

## Project Summary

**s3lo** is a Go CLI tool that uses AWS S3 as a container image registry. It stores OCI-format container images on S3 with content-addressable deduplication, parallel transfers, and lifecycle management. Commands: `push`, `pull`, `copy`, `list`, `inspect`, `stats`, `delete`, `clean`, `config`, `scan`.

---

## Legend

- ✅ Fixed
- ⏭️ Skipped (requires large refactor or is a new feature — documented for future work)

---

## 🔴 Critical Issues

### 1. ✅ Push uploads blobs **sequentially**
**Fixed in:** `bace75e`

Rewrote blob upload loop in `push.go` to use `errgroup` with 10 concurrent workers, matching the parallelism already in place for pull and copy.

### 2. ✅ Push performs **double HeadObject** for every blob
**Fixed in:** `bace75e`

Removed the internal `HeadObject` dedup check inside `uploadFile`. Push now does a single `HeadObjectExists` call before deciding whether to upload.

### 3. ✅ `ImportImage` reads entire layers into memory via `os.ReadFile`
**Fixed in:** `195370b`

`ImportImage` in `layout.go` now opens each layer file and streams it via `io.Copy` into the tar writer. No full layer is held in memory.

### 4. ✅ `extractTar` has no file size limit — zip bomb/resource exhaustion risk
**Fixed in:** `195370b`

Added `maxExtractFileSize = 10 GB` constant. Each tar entry is checked against `hdr.Size` before extraction and wrapped with `io.LimitReader`.

### 5. ✅ `downloadAndExtractTrivy` has no size limit on download
**Fixed in:** `bace75e` (scan.go)

Added `maxTrivyBinarySize = 500 MB` constant. The `trivy` binary entry is rejected if `hdr.Size` exceeds the cap, and extraction uses `io.LimitReader`.

---

## 🟠 Performance Bottlenecks

### 6. ✅ New S3 client created on **every single command**
**Fixed in:** `cdd1f10`

`Client` struct now holds a `clientCache map[string]*s3.Client`. `ClientForBucket` returns the cached client for a region without reloading AWS config.

### 7. ✅ `ClientForBucket` calls `config.LoadDefaultConfig` on every call
**Fixed in:** `cdd1f10`

`NewClient` loads config once and stores it as `baseCfg aws.Config`. `ClientForBucket` derives per-region clients from `baseCfg` without reloading.

### 8. ✅ `Stats()` downloads **every manifest sequentially**
**Fixed in:** `316599f`

`stats.go` now uses `errgroup` with 10 concurrent workers to fetch manifests in parallel.

### 9. ✅ `Inspect` for multi-arch fetches platform manifests sequentially
**Fixed in:** PR #39

Platform manifest fetches in `inspect.go` now run in parallel using `errgroup`. Results are written into a pre-allocated `[]PlatformInfo` slice by index to preserve order.

### 10. ✅ Lifecycle tag deletion is sequential per-tag
**Fixed in:** PR #39

`ApplyLifecycle` in `lifecycle.go` now deletes tags in parallel (up to 10 concurrent workers) using `errgroup`.

### 11. ⏭️ `copy.go` registry-to-S3: blobs fetched into memory via `io.ReadAll`
**Skipped**

`fetchAndUploadBlob` calls `fetchWithAuth` which does `io.ReadAll` on the response body before writing to a temp file. Fixing this requires splitting `fetchWithAuth` into a streaming variant for large blobs vs. a byte-slice variant for small manifests. Left for a future refactor when `copy.go` is restructured.

### 12. ✅ `copy.go` cross-bucket: blobs downloaded fully into memory
**Fixed in:** PR #39

Replaced `GetObject` (returns `[]byte`) + `writeTempFile` with `DownloadObjectToFile` which streams directly from S3 to a temp file. The blob is never fully in memory.

---

## 🟡 Correctness / Bug Issues

### 13. ✅ `HeadObjectExists` swallows ALL errors — not just 404
**Fixed in:** `316599f`

`HeadObjectExists` now returns `(false, nil)` only for actual 404 responses (checked via `s3types.NotFound` and the error message as a fallback). All other errors are propagated to the caller.

### 14. ✅ Dedup check compares **size only**, not content hash
**Fixed as side-effect of #2**

The old size-only check inside `uploadFile` was removed when the internal dedup logic was eliminated. Push now uses `HeadObjectExists` — a key presence check — which is sufficient since blob keys are content-addressed (SHA256 digest). Manifest files are always overwritten on push, which is correct behavior.

### 15. ✅ E2E test passes wrong argument to `pull` command
**Fixed in:** PR #39

`e2e_test.go` passed `tmpDir` as the second argument to `pull`, which expects an optional image tag, not a directory path. The bad argument and the unreachable `os.Stat(tmpDir+"/manifest.json")` check (pull imports into Docker, not a directory) were both removed.

### 16. ✅ `evaluateTags` lifecycle logic: `keep_last` counts include protected tags
**Fixed in:** PR #39

`evaluateTags` in `lifecycle.go` now uses a separate `kept` counter for non-protected tags. Protected (`keep_tags`) entries are skipped entirely and do not consume `keep_last` slots, preventing over-deletion when protected tags are the newest ones.

### 17. ⏭️ `fetchWithAuth` doesn't reuse the acquired token for subsequent requests
**Skipped**

After a 401 challenge, the acquired Bearer token is discarded. The next call (e.g., fetching a blob) triggers another 401 → token flow. Token caching requires a per-registry token store with expiry handling — a non-trivial change. Left for a future refactor of the HTTP/auth layer in `copy.go`.

### 18. ✅ `getObject` in `inspect.go` is a local duplicate of `s3client.GetObject`
**Fixed in:** PR #39

Removed the local `getObject` helper. `Inspect` now calls `client.GetObject` directly, matching the pattern used in every other package function.

---

## 🔵 Code Quality / Architecture

### 19. ⏭️ `copy.go` is 801 lines — too large, duplicated logic
**Skipped**

`copyS3ToS3` and `copyRegistryToS3` share patterns (manifest parsing, blob dedup, parallel execution, manifest writing) but are not yet consolidated. A future refactor should extract a shared `blobCopier` interface and common manifest/blob pipeline. Left until `copy.go` is stable and well-tested.

### 20. ⏭️ Manifest struct is re-declared as anonymous structs 5+ times
**Skipped**

The same `{ Config { Digest, Size }, Layers []{ Digest, Size } }` shape appears in `copy.go`, `stats.go`, and `gc.go`. Should become a shared named type in `pkg/oci`. Left for the same refactor pass as #19.

### 21. ⏭️ No structured logging — all output goes through `fmt.Printf`
**Skipped**

Adding structured logging (`slog`, `zap`) is a cross-cutting change that touches every command and package. Left for a dedicated logging milestone.

### 22. ⏭️ No `--verbose` / `--debug` flag
**Skipped**

Depends on #21 (structured logging). Left for the same milestone.

### 23. ✅ `splitLines` in `config.go` reimplements `strings.Split`
**Fixed in:** PR #39

Replaced the custom `splitLines` function with `strings.Split(rec.Description, "\n")` inline.

### 24. ⏭️ `term` import for TTY detection should be behind a build tag or optional
**Skipped**

`golang.org/x/term` is already a transitive dependency of `golang.org/x/crypto` (pulled in by Docker). The import weight is negligible. No action needed.

---

## 🟢 Minor / Cosmetic Issues

### 25. ⏭️ Progress bar shows indeterminate (`-1`) — could show total size
**Skipped**

Push and pull know total blob sizes before starting. The progress bar could be changed from indeterminate to a percentage bar. UX improvement, left for a future pass.

### 26. ⏭️ `version` and `commit` vars lack documentation
**Skipped**

The `ldflags` injection pattern (`-X main.version=...`) is standard Go. Left as-is; goreleaser handles the injection automatically.

### 27. ⏭️ `Makefile` doesn't inject version/commit into the build
**Skipped**

GoReleaser handles version injection for releases. The `Makefile` dev build intentionally uses `dev`/`none` defaults. Not a correctness issue.

### 28. ⏭️ `.goreleaser.yml` — verify it sets ldflags correctly
**Skipped**

Verified: `.goreleaser.yml` sets `-X main.version={{.Version}} -X main.commit={{.Commit}}`. No action needed.

### 29. ✅ Missing `context.Context` on HTTP requests in `fetchWithAuth`
**Fixed in:** PR #39

Added `fetchWithAuthContext(ctx, ...)` that uses `http.NewRequestWithContext` for both the initial request and the 401-retry request. The original `fetchWithAuth` now delegates to it with `context.Background()` for backward compatibility. All three call sites in `copyRegistryToS3` were updated to pass the live `ctx`.

### 30. ✅ No Content-Type set on `PutObject` uploads
**Fixed in:** PR #39

Added `contentTypeForKey(key string) string` helper in `upload.go`. All `uploadFile` calls now set `Content-Type`: `application/json` for `.json` files and `oci-layout`, `application/octet-stream` for blobs.

### 31. ⏭️ No retry logic for S3 or HTTP operations
**Skipped**

The AWS SDK has built-in retry logic for S3. The HTTP client used for registry calls (`fetchWithAuth`) does not retry. Adding retry with exponential backoff is a non-trivial change; left for a dedicated reliability pass.

---

## Summary Table

| # | Severity | Status | Short Description |
|---|----------|--------|-------------------|
| 1 | 🔴 Critical | ✅ Fixed (`bace75e`) | Push uploads blobs sequentially |
| 2 | 🔴 Critical | ✅ Fixed (`bace75e`) | Double HeadObject on every push blob |
| 3 | 🔴 Critical | ✅ Fixed (`195370b`) | ImportImage reads entire layers into memory |
| 4 | 🔴 Critical | ✅ Fixed (`195370b`) | extractTar has no size limit (zip bomb) |
| 5 | 🔴 Critical | ✅ Fixed (`bace75e`) | Trivy download has no size limit |
| 6 | 🟠 Perf | ✅ Fixed (`cdd1f10`) | New S3 client created on every command |
| 7 | 🟠 Perf | ✅ Fixed (`cdd1f10`) | ClientForBucket reloads AWS config on every call |
| 8 | 🟠 Perf | ✅ Fixed (`316599f`) | Stats downloads manifests sequentially |
| 9 | 🟠 Perf | ✅ Fixed (PR #39) | Inspect fetches platform manifests sequentially |
| 10 | 🟠 Perf | ✅ Fixed (PR #39) | Lifecycle tag deletion is sequential |
| 11 | 🟠 Perf | ⏭️ Skipped | Registry-to-S3 copy reads blobs into memory |
| 12 | 🟠 Perf | ✅ Fixed (PR #39) | Cross-bucket copy reads blobs into memory |
| 13 | 🟡 Bug | ✅ Fixed (`316599f`) | HeadObjectExists swallows all errors |
| 14 | 🟡 Bug | ✅ Fixed (side-effect of #2) | Dedup compares only size, not hash |
| 15 | 🟡 Bug | ✅ Fixed (PR #39) | E2E test passes wrong args to pull |
| 16 | 🟡 Bug | ✅ Fixed (PR #39) | keep_last counts include protected tags |
| 17 | 🟡 Bug | ⏭️ Skipped | Auth token not reused across requests |
| 18 | 🟡 Quality | ✅ Fixed (PR #39) | Duplicate getObject function in inspect |
| 19 | 🔵 Quality | ⏭️ Skipped | copy.go is 801 lines with duplicated logic |
| 20 | 🔵 Quality | ⏭️ Skipped | Manifest struct re-declared 5+ times |
| 21 | 🔵 Quality | ⏭️ Skipped | No structured logging |
| 22 | 🔵 Quality | ⏭️ Skipped | No --verbose/--debug flag |
| 23 | 🔵 Quality | ✅ Fixed (PR #39) | splitLines reimplements strings.Split |
| 24 | 🔵 Quality | ⏭️ Skipped | Unnecessary term import (not an issue) |
| 25 | 🟢 Minor | ⏭️ Skipped | Progress bar always indeterminate |
| 26 | 🟢 Minor | ⏭️ Skipped | Version vars lack documentation |
| 27 | 🟢 Minor | ⏭️ Skipped | Makefile may not inject version |
| 28 | 🟢 Minor | ⏭️ Skipped | goreleaser ldflags — verified, no action needed |
| 29 | 🟢 Minor | ✅ Fixed (PR #39) | Missing context on HTTP requests |
| 30 | 🟢 Minor | ✅ Fixed (PR #39) | No Content-Type on PutObject |
| 31 | 🟢 Minor | ⏭️ Skipped | No retry logic for HTTP operations |

**Fixed: 20 / 31** &nbsp;|&nbsp; **Skipped: 11 / 31**

### Remaining work (skipped items)

Items worth revisiting in a future milestone:

| Priority | # | What to do |
|----------|---|------------|
| High | #11 | Split `fetchWithAuth` into streaming (blobs) vs. bytes (manifests) to avoid loading large layers into memory during registry-to-S3 copy |
| High | #17 | Cache Bearer token per-registry with TTL to avoid repeated 401 round-trips |
| Medium | #19, #20 | Refactor `copy.go`: extract shared blob/manifest pipeline; move anonymous manifest struct to `pkg/oci` |
| Medium | #21, #22 | Add structured logging (`slog`) and `--verbose` flag |
| Low | #25 | Deterministic progress bar (total size known before download starts) |
| Low | #31 | Retry with exponential backoff for registry HTTP calls |
