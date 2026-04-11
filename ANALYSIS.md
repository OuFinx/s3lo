# s3lo Codebase Analysis Report

## Project Summary

**s3lo** is a Go CLI tool that uses AWS S3 as a container image registry. It stores OCI-format container images on S3 with content-addressable deduplication, parallel transfers, and lifecycle management. Commands: `push`, `pull`, `copy`, `list`, `inspect`, `stats`, `delete`, `clean`, `config`, `scan`.

---

## 🔴 Critical Issues

### 1. Push uploads blobs **sequentially** — defeats the purpose of S3 parallelism
**File:** [push.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/push.go#L79-L104)

Blob uploads in `Push()` iterate one-by-one in a `for` loop. Pull uses `errgroup` with 10 workers, copy does too — but push does not. For images with many layers, this is a major bottleneck.

### 2. Push performs **double HeadObject** for every blob (redundant API calls)
**File:** [push.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/push.go#L88-L98) + [upload.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/s3/upload.go#L68-L77)

`Push()` calls `HeadObjectExists()` to check dedup, then calls `UploadFile()` which internally calls `HeadObject` again. Every blob gets 2× the HEAD requests needed.

### 3. `ImportImage` reads entire layers into memory via `os.ReadFile`
**File:** [layout.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/oci/layout.go#L296-L309)

```go
layerData, err := os.ReadFile(layerPath) // OOM risk for large layers
```

Each layer (often 100s of MB) is fully loaded into memory, then written to a tar. This can easily cause OOM on machines with limited RAM. Should stream from file instead.

### 4. `extractTar` has no file size limit — zip bomb/resource exhaustion risk
**File:** [layout.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/oci/layout.go#L170-L208)

No limit on individual file sizes or total extracted size. A malicious tar could exhaust disk or memory. Should use `io.LimitReader`.

### 5. `downloadAndExtractTrivy` has no size limit on download
**File:** [scan.go (cmd)](file:///Users/finx/Work/Other/vibe/s3lo/cmd/s3lo/scan.go#L206-L256)

Downloads an arbitrary tarball from GitHub with `io.Copy(tmp, tr)` and no size limit. A redirected/MITM'd response could exhaust disk.

---

## 🟠 Performance Bottlenecks

### 6. New S3 client created on **every single command** — no client reuse
**Files:** [push.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/push.go#L51), [pull.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/pull.go#L35), [delete.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/delete.go#L19), [inspect.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/inspect.go#L53), etc.

Every function calls `s3client.NewClient(ctx)` which does `config.LoadDefaultConfig` (reads env/files, makes HTTP calls for IMDS). The client should be created once and passed in or cached.

### 7. `ClientForBucket` calls `config.LoadDefaultConfig` on every call
**File:** [client.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/s3/client.go#L42-L47)

Even when the region is cached, `ClientForBucket` still calls `config.LoadDefaultConfig` and creates a brand-new `s3.Client`. Should cache the per-region clients.

### 8. `Stats()` downloads **every manifest sequentially** to compute logical bytes
**File:** [stats.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/stats.go#L60-L95)

For each manifest key, it calls `client.GetObject` one-by-one in a for loop. For buckets with many images, this is extremely slow. Should parallelize like `collectReferencedDigests` does in `gc.go`.

### 9. `Inspect` for multi-arch fetches platform manifests sequentially
**File:** [inspect.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/inspect.go#L86-L108)

Each platform manifest is fetched one at a time. Should use `errgroup` for parallel fetches.

### 10. Lifecycle tag deletion is sequential per-tag
**File:** [lifecycle.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/lifecycle.go#L115-L126)

Each tag to delete requires a list+delete cycle done sequentially. Could batch across all tags.

### 11. `copy.go` registry-to-S3: blobs fetched into memory, written to temp file, then uploaded
**File:** [copy.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/copy.go#L370-L398)

`fetchAndUploadBlob` reads the entire blob into memory (`io.ReadAll`), writes to temp file, then uploads. For large layers (GBs), this doubles memory usage. Should stream directly.

### 12. `copy.go` cross-bucket: blobs downloaded fully into memory
**File:** [copy.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/copy.go#L99-L110)

Cross-bucket S3 copy downloads the entire blob into memory via `GetObject`, writes to temp file, then uploads. Same streaming issue as #11.

---

## 🟡 Correctness / Bug Issues

### 13. `HeadObjectExists` swallows ALL errors — not just 404
**File:** [upload.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/s3/upload.go#L124-L128)

```go
if err != nil {
    return false, nil // treat any error (including 404) as not exists
}
```

Network errors, permission errors, throttling — all treated as "not exists". This could cause re-uploads of existing blobs or mask permission issues.

### 14. Dedup check in `uploadFile` only compares **size**, not content hash
**File:** [upload.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/s3/upload.go#L72-L77)

```go
if head.ContentLength != nil && *head.ContentLength == info.Size() {
    return nil // skip
}
```

Relies solely on size match. While blob keys are content-addressed (SHA256), manifest files and other non-blob files use the same function but aren't content-addressed — a manifest overwrite with different content but same size would be silently skipped.

### 15. E2E test passes wrong argument to `pull` command
**File:** [e2e_test.go](file:///Users/finx/Work/Other/vibe/s3lo/e2e_test.go#L54-L56)

```go
pull := exec.Command(binary, "pull", ref, tmpDir)
```

But `pull` expects `[s3-ref] [image-tag]`, not a directory. `tmpDir` is being passed as an image tag name, not a destination directory. The pulled image goes into Docker with the temp dir path as its tag name, and the test then checks for `manifest.json` in the temp dir — which would never exist.

### 16. `evaluateTags` lifecycle logic: `keep_last` counts include protected tags
**File:** [lifecycle.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/lifecycle.go#L158-L173)

Tags in `keep_tags` are skipped but still counted toward the `keep_last` index position. If `keep_last=2` and the 2 newest tags are both in `keep_tags`, all remaining tags would be deleted even if they should be kept.

### 17. `fetchWithAuth` doesn't reuse the acquired token for subsequent requests
**File:** [copy.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/copy.go#L625-L676)

After a 401 challenge, the function acquires a Bearer token and retries once, but discards it. The next call to `fetchWithAuth` for the same registry (e.g., fetching blob data) will trigger another 401 → token flow. The token should be cached.

### 18. `GetObject` in `inspect.go` is a local duplicate of `s3client.GetObject`
**File:** [inspect.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/inspect.go#L137-L147)

There's a local `getObject` function that duplicates `s3client.Client.GetObject`. This creates inconsistency — inspect uses the raw SDK client while other functions go through the s3client wrapper.

---

## 🔵 Code Quality / Architecture

### 19. `copy.go` is 801 lines — too large, duplicated logic
**File:** [copy.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/copy.go)

`copyS3ToS3` and `copyRegistryToS3` have nearly identical manifest parsing, blob dedup, parallel execution, and manifest writing code. The `blobTask` struct and `runParallel` closure are defined separately in each function. Should extract common logic.

### 20. Manifest struct is re-declared as anonymous structs 5+ times
**Files:** `copy.go` (4 times), `stats.go`, `gc.go`

The same `{ Config { Digest, Size }, Layers []{ Digest, Size } }` anonymous struct is defined repeatedly. Should be a shared type.

### 21. No structured logging — all output goes through `fmt.Printf`
All CLI commands use `fmt.Printf` to stderr/stdout with no log levels, no structured fields, no debug mode. Makes troubleshooting in production/CI difficult.

### 22. No `--verbose` / `--debug` flag
No way to get detailed output about what's happening (which blobs are being skipped, region detection, auth flows, etc.).

### 23. `splitLines` in `config.go` reimplements `strings.Split`
**File:** [config.go](file:///Users/finx/Work/Other/vibe/s3lo/cmd/s3lo/config.go#L359-L372)

Custom `splitLines` function could just be `strings.Split(s, "\n")`.

### 24. `term` import for TTY detection should be behind a build tag or optional
**File:** [scan.go](file:///Users/finx/Work/Other/vibe/s3lo/cmd/s3lo/scan.go#L19)

`golang.org/x/term` is imported for `term.IsTerminal()` in a single function. This adds a dependency for a minor feature.

---

## 🟢 Minor / Cosmetic Issues

### 25. Progress bar shows indeterminate (`-1`) — could show total size
**File:** [format.go](file:///Users/finx/Work/Other/vibe/s3lo/cmd/s3lo/format.go#L16)

Push/pull know the total blob count and sizes from the manifest before downloading. The progress bar could show actual progress percentage.

### 26. `version` and `commit` vars use `var` instead of `ldflags` pattern documentation
**File:** [root.go](file:///Users/finx/Work/Other/vibe/s3lo/cmd/s3lo/root.go#L9-L12)

While the vars exist for linker injection, there are no comments or build documentation explaining how to set them.

### 27. `Makefile` doesn't inject version/commit into the build
**File:** `Makefile` (not read but referenced)

Should be verified — the build should pass `-ldflags` to set version/commit.

### 28. `.goreleaser.yml` — should verify it sets ldflags correctly
**File:** `.goreleaser.yml`

### 29. Missing `context.Context` on HTTP requests in `fetchWithAuth`
**File:** [copy.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/image/copy.go#L627)

Uses `http.NewRequest` instead of `http.NewRequestWithContext`. The retry request on line 653 also lacks context. This means these requests are not cancellable.

### 30. No Content-Type set on `PutObject` uploads
**File:** [upload.go](file:///Users/finx/Work/Other/vibe/s3lo/pkg/s3/upload.go#L86-L95)

S3 `PutObject` doesn't set a `ContentType`. Manifests would benefit from `application/json` and blobs from `application/octet-stream`.

### 31. No retry logic for S3 or HTTP operations
All S3 and HTTP operations are fire-once. Network transient errors (throttling, 5xx) will cause immediate failure. The AWS SDK has built-in retries, but the HTTP client for registries does not.

---

## Summary Table

| # | Severity | Category | Short Description |
|---|----------|----------|-------------------|
| 1 | 🔴 Critical | Performance | Push uploads blobs sequentially |
| 2 | 🔴 Critical | Performance | Double HeadObject on every push blob |
| 3 | 🔴 Critical | Memory | ImportImage reads entire layers into memory |
| 4 | 🔴 Critical | Security | extractTar has no size limit (zip bomb) |
| 5 | 🔴 Critical | Security | Trivy download has no size limit |
| 6 | 🟠 Perf | Performance | New S3 client created on every command |
| 7 | 🟠 Perf | Performance | ClientForBucket reloads AWS config on every call |
| 8 | 🟠 Perf | Performance | Stats downloads manifests sequentially |
| 9 | 🟠 Perf | Performance | Inspect fetches platform manifests sequentially |
| 10 | 🟠 Perf | Performance | Lifecycle tag deletion is sequential |
| 11 | 🟠 Perf | Memory | Registry-to-S3 copy reads blobs into memory |
| 12 | 🟠 Perf | Memory | Cross-bucket copy reads blobs into memory |
| 13 | 🟡 Bug | Correctness | HeadObjectExists swallows all errors |
| 14 | 🟡 Bug | Correctness | Dedup compares only size, not hash |
| 15 | 🟡 Bug | Correctness | E2E test passes wrong args to pull |
| 16 | 🟡 Bug | Correctness | keep_last counts include protected tags |
| 17 | 🟡 Bug | Correctness | Auth token not reused across requests |
| 18 | 🟡 Quality | Code Quality | Duplicate getObject function in inspect |
| 19 | 🔵 Quality | Architecture | copy.go is 801 lines with duplicated logic |
| 20 | 🔵 Quality | Architecture | Manifest struct re-declared 5+ times |
| 21 | 🔵 Quality | Architecture | No structured logging |
| 22 | 🔵 Quality | UX | No --verbose/--debug flag |
| 23 | 🔵 Quality | Code Quality | splitLines reimplements strings.Split |
| 24 | 🔵 Quality | Dependencies | Unnecessary term import |
| 25 | 🟢 Minor | UX | Progress bar always indeterminate |
| 26 | 🟢 Minor | Docs | Version vars lack documentation |
| 27 | 🟢 Minor | Build | Makefile may not inject version |
| 28 | 🟢 Minor | Build | goreleaser ldflags verification |
| 29 | 🟢 Minor | Correctness | Missing context on HTTP requests |
| 30 | 🟢 Minor | S3 | No Content-Type on PutObject |
| 31 | 🟢 Minor | Reliability | No retry logic for HTTP operations |
