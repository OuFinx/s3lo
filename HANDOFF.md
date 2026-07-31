# Handoff: v2.0.1 out, operator v1.5.0 out

Untracked on purpose — working notes, like `IDEAS.md` and `TESTING-REPORT.md`.

## Done

**s3lo**

- **Benchmark re-measured** on a fresh `c6id.xlarge`, all four sizes, account
  torn down and verified the same session (PR #106). Pull now wins everywhere:
  0.80 / 2.83 / 8.11 / 15.14 s against ECR's 1.46 / 4.81 / 9.87 / 15.65 s at
  99 / 499 / 999 / 1795 MB.
- **`v2.0.0`** tagged and released — binaries and homebrew tap.
- **`v2.0.1`** tagged and released. `v2.0.0` was not importable: `go.mod` said
  `github.com/OuFinx/s3lo` while Go requires the major in the path from v2 on, so
  `go get` rejected the tag. PR #107 added `/v2` and rewrote 81 self-imports.
  Verified: `go get github.com/OuFinx/s3lo/v2@v2.0.1` resolves.

`v2.0.0` remains published as a binary release, simply not importable. Nothing
depended on it in the hour it stood alone.

**s3lo-operator**

- **PR #28 merged**: the proxy's duplicated OCI handling, manifest resolution,
  blob assembly and digest verification deleted in favour of
  `github.com/OuFinx/s3lo/v2/pkg/serve`. 387 insertions against 2104 deletions.
  What remains in `pkg/proxy` is multi-bucket routing, the Kubernetes setup and
  the Prometheus metrics.
- **PR #29 merged**: roadmap and chart brought back in line — v1.4.0 and v1.4.1
  had no roadmap entries, a `v2.0.0 — Security` milestone was recorded for work
  that shipped inside v1.3.0, the chart was still on 1.4.0 after v1.4.1, and the
  guide answered "is there a metrics endpoint?" with "not yet".
- **`v1.5.0`** tagged. Release and Publish-Helm-Chart workflows both green.
  The GHCR package listing needs a `read:packages` token, so the published image
  and chart tags were not confirmed by hand — only the workflows were.

## Backlog

Issues #100 (bucket clean silently no-ops with a prefix — the only bug), #101,
#102, #103, #94, #95.

## Notes for next time

- `rtk` truncated `grep` output during this session and hid a file that still
  held old import paths. It surfaced only because the compiler failed. Do not
  trust a filtered `grep` for completeness — use `find -exec` when it matters.
- The release workflow warns about Node 20 deprecation in `actions/checkout@v4`,
  `actions/setup-go@v5` and `goreleaser-action@v6`, and about `version: latest`
  in goreleaser-action. Neither is breaking yet.

## If the benchmark is ever re-run

The instance recipe from the earlier handoff still holds, plus:

- AL2023's `/tmp` is a 3.9 GB tmpfs. `pip download` stages there and dies with
  ENOSPC — set `TMPDIR=/mnt/fast/tmp` first.
- AL2023's default `pip3` is Python 3.9. Install `python3.11` and use
  `python3.11 -m pip`, or wheels resolve to old versions.
- Drive the instance with `aws ssm send-command`: no session-manager-plugin, no
  key pair, no inbound rule. SSM truncates captured output at 2500 chars, so log
  to a file and fetch it separately.
- The 64 MB per-file skip in `build_payload` is the real cap on payload size, not
  the disk — a 9 GB wheel corpus yielded only 1852 MB of usable material.
