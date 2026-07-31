# s3lo Review Findings

Date: 2026-04-14

## What This Application Does

`s3lo` is a Go CLI that stores OCI container images in object storage instead of a traditional registry.

- CLI entrypoints live under [cmd/s3lo](/Users/finx/Work/Other/vibe/s3lo/cmd/s3lo).
- Image workflows live under [pkg/image](/Users/finx/Work/Other/vibe/s3lo/pkg/image).
- OCI export/import helpers live under [pkg/oci](/Users/finx/Work/Other/vibe/s3lo/pkg/oci).
- Storage backends live under [pkg/storage](/Users/finx/Work/Other/vibe/s3lo/pkg/storage).
- The optional registry-compatible HTTP server lives under [pkg/serve](/Users/finx/Work/Other/vibe/s3lo/pkg/serve).

At a high level:

- `push` exports a local Docker image, writes blobs into `blobs/sha256/...`, and writes tag metadata into `manifests/<image>/<tag>/`.
- `pull` reads `manifest.json`, downloads the referenced blobs, reconstructs an OCI layout locally, then `docker load`s it.
- `copy` can mirror images from a registry or another backend into this layout without going through the local Docker daemon.
- `clean`, `doctor`, `stats`, `history`, `sign`, `verify`, `scan`, and `serve` are management layers built on top of that stored layout.

I also ran `go test ./...`; the current test suite passes.

## Priority Findings

### P1 - `clean` / `doctor` are unsafe for multi-arch images

Relevant code:

- [pkg/image/gc.go:81](/Users/finx/Work/Other/vibe/s3lo/pkg/image/gc.go:81)
- [pkg/image/doctor.go:73](/Users/finx/Work/Other/vibe/s3lo/pkg/image/doctor.go:73)
- [pkg/image/doctor.go:148](/Users/finx/Work/Other/vibe/s3lo/pkg/image/doctor.go:148)

Both `GC` and `Doctor` assume every `manifests/<image>/<tag>/manifest.json` is a single-arch OCI manifest with `config` and `layers`. That is false for copied multi-arch images, where the top-level `manifest.json` is an OCI index and the child platform manifests live in `blobs/sha256/...`.

Current behavior:

- `GC` never traverses index entries, so platform manifest blobs, config blobs, and layer blobs for valid multi-arch tags are not marked as referenced.
- After the one-hour grace period, `s3lo clean --blobs --confirm` can delete live data for a healthy multi-arch image.
- `Doctor` has the same blind spot, so it can report false orphaned blobs while also failing to detect missing child manifests or child blobs.

This is the most serious issue in the repository because it turns a supported feature into a destructive maintenance path.

Fix direction:

- Add one shared recursive walker that understands both OCI manifests and OCI indexes.
- Reuse that helper in `GC`, `Doctor`, `Stats`, `History`, and size-related validation.

### P1 - `serve` cannot fully serve multi-arch images

Relevant code:

- [pkg/serve/handler.go:15](/Users/finx/Work/Other/vibe/s3lo/pkg/serve/handler.go:15)
- [pkg/serve/handler.go:52](/Users/finx/Work/Other/vibe/s3lo/pkg/serve/handler.go:52)
- [pkg/serve/server_test.go:124](/Users/finx/Work/Other/vibe/s3lo/pkg/serve/server_test.go:124)

The server resolves `/v2/<name>/manifests/<digest>` by scanning only `manifests/<name>/*/manifest.json`. That works for top-level tag manifests, but it does not work for platform manifests inside a multi-arch image because those are stored only under `blobs/sha256/<digest>`.

Why this matters:

- OCI clients typically fetch the tag, receive an image index, then fetch the selected platform manifest by digest through the `manifests` endpoint.
- `s3lo serve` therefore looks "docker pull compatible" for single-arch images but is incomplete for multi-arch images.
- The current serve tests only cover single-arch tag and digest lookups, so this path is not exercised.

Fix direction:

- When the request is `GET/HEAD /manifests/<sha256:...>`, resolve digest lookups against both top-level tag manifests and platform manifest blobs.
- Add explicit multi-arch server tests.

### P1 - `copy` bypasses immutability enforcement

Relevant code:

- [pkg/image/push.go:60](/Users/finx/Work/Other/vibe/s3lo/pkg/image/push.go:60)
- [pkg/image/copy_registry.go:22](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_registry.go:22)
- [pkg/image/copy_s3.go:21](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_s3.go:21)

`push` explicitly checks bucket config and blocks overwriting immutable tags. `copy` does not do that at all, whether the source is another backend or a registry.

Impact:

- An "immutable" tag can still be replaced by `s3lo copy ... dest:tag`.
- That undermines the main safety promise behind the immutability setting.
- There is no `--force` escape hatch on `copy`, so the current behavior is not even clearly intentional policy.

Fix direction:

- Move the destination-tag write policy into a shared helper and call it from both `push` and `copy`.

### P2 - Size, cost, and history accounting are wrong for image indexes

Relevant code:

- [pkg/image/stats.go:96](/Users/finx/Work/Other/vibe/s3lo/pkg/image/stats.go:96)
- [pkg/image/history.go:175](/Users/finx/Work/Other/vibe/s3lo/pkg/image/history.go:175)
- [pkg/image/copy_registry.go:243](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_registry.go:243)
- [pkg/image/copy_s3.go:217](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_s3.go:217)
- [pkg/image/validate.go:153](/Users/finx/Work/Other/vibe/s3lo/pkg/image/validate.go:153)

Several reporting paths reuse single-arch assumptions:

- `Stats` sums only `config.size + layer.size` from the top-level `manifest.json`. For an OCI index, that becomes zero.
- `totalManifestSize()` does the same, so `copy` records `size_bytes=0` for multi-arch tags.
- `runSizePolicy()` uses the largest single platform, not the total logical size of the image set, even though the policy is described as a total size check.

Impact:

- `stats` understates logical bytes, dedup savings, and projected cost for buckets containing multi-arch images.
- `history` output is misleading for copied multi-arch tags.
- Size-based policies can pass images that exceed the documented limit once all platforms are considered.

Fix direction:

- Decide what "size" means for multi-arch tags in each command.
- Implement that definition once in shared code rather than re-parsing manifests ad hoc in each package.

### P2 - The `signed` policy is only checking for file presence, not trust

Relevant code:

- [pkg/image/validate.go:137](/Users/finx/Work/Other/vibe/s3lo/pkg/image/validate.go:137)
- [pkg/image/verify.go:35](/Users/finx/Work/Other/vibe/s3lo/pkg/image/verify.go:35)

`runSignedPolicy()` passes as soon as any file exists under `manifests/<image>/<tag>/signatures/`. It does not parse the signature record, does not compare the signed digest to the current manifest, and does not verify the cryptographic signature. The repository already has a real verifier in `Verify()`, but policy validation is not using it.

Impact:

- A stale, corrupted, or manually dropped JSON file can satisfy the "signed" policy.
- The current command output overstates the security posture because "signed" does not mean "verified."

Fix direction:

- Either change the policy model to include a trusted key and delegate to `Verify()`, or rename/document the policy so it clearly means "signature file exists" rather than "image is signed."

### P3 - Important storage and history errors are being silently discarded

Relevant code:

- [pkg/image/push.go:118](/Users/finx/Work/Other/vibe/s3lo/pkg/image/push.go:118)
- [pkg/image/copy_registry.go:57](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_registry.go:57)
- [pkg/image/copy_registry.go:170](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_registry.go:170)
- [pkg/image/copy_s3.go:49](/Users/finx/Work/Other/vibe/s3lo/pkg/image/copy_s3.go:49)
- [pkg/image/history.go:219](/Users/finx/Work/Other/vibe/s3lo/pkg/image/history.go:219)

There are several places where the code intentionally ignores backend errors:

- Blob existence checks in `push` and both `copy` implementations drop `HeadObjectExists()` errors and treat them as "object missing."
- `recordHistory()` drops `readHistory()` errors and rewrites history from scratch.

Impact:

- IAM problems, transient storage failures, or backend outages can turn into misleading dedup decisions instead of visible failures.
- A broken or unreadable `history.json` can silently lose the previous audit trail on the next write.

Fix direction:

- Propagate hard backend failures.
- If history is intentionally best-effort, preserve the old file on read/parse failure instead of overwriting it with a truncated version.

## Test Coverage Gaps

The current suite gives decent coverage for single-arch flows, but it does not protect the riskiest modern paths:

- No tests for multi-arch `serve`.
- No tests for multi-arch `doctor`.
- No tests for multi-arch `clean` / `GC`.
- No tests for multi-arch `stats` or `history` accounting.

That gap explains why the repository can pass `go test ./...` while still having serious multi-arch regressions.

## Recommended Order Of Fixes

1. Fix the multi-arch reference walker and wire it into `GC` and `Doctor`.
2. Fix multi-arch manifest serving in `pkg/serve`.
3. Enforce immutability in `copy`.
4. Correct size/accounting semantics for multi-arch tags.
5. Tighten signature policy validation.
6. Remove the silent error swallowing paths and add regression tests around them.
