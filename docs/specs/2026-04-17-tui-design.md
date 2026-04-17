# TUI Design Spec — `s3lo tui`

**Date:** 2026-04-17
**Issue:** #55
**Milestone:** v1.13.0 (or dedicated v1.14.0 if scoped separately)

---

## 1. Overview

`s3lo tui s3://my-bucket/` opens an interactive terminal UI for browsing and managing OCI images stored in an S3 bucket. It provides the same operations as the CLI (list, delete, inspect, scan, clean) through a keyboard-driven interface.

---

## 2. Architecture

### Package layout

```
pkg/tui/
  model.go          — RootModel: top-level bubbletea model
  imagelist.go      — ImageListPane model
  taglist.go        — TagListPane model
  statspanel.go     — StatsPanel model (right pane)
  overlays.go       — ConfirmDialog, InspectView, ScanResults, CleanPreview
  messages.go       — all Msg types
  commands.go       — all tea.Cmd functions (S3 calls)
  styles.go         — lipgloss style definitions

cmd/s3lo/tui.go     — cobra command, wires storage + RootModel, runs tea.NewProgram
```

### RootModel structure

```go
type RootModel struct {
    storage  storage.Backend
    leftPane leftPane        // interface: ImageListPane | TagListPane
    right    StatsPanel
    overlay  tea.Model       // nil when no overlay active
    status   string          // status bar message (clears after 4s)
    err      error           // fatal error (renders error screen)
    width    int
    height   int
}
```

`leftPane` is an interface with `Update`, `View`, and `SelectedItem() any` methods. Swapping it (bucket → image → back) is done by replacing the field on `RootModel` — no navigation stack needed since there are only two levels.

---

## 3. Layout

Two-pane split: left pane takes ~60% width, right pane ~40%.

**Mode 1 — Bucket view:**
- Left: image list (name, tag count, size)
- Right: bucket stats (total images/tags/blobs, total size, logical size, dedup savings, estimated cost, ECR equivalent cost)

**Mode 2 — Image view (after Enter on an image):**
- Left: tag list sorted newest→oldest (tag name, age, size, signed indicator)
- Right: per-tag stats for the highlighted tag (layers, architectures, signed status, cost) — loaded on demand, spinner while fetching

Bottom bar: context-sensitive key hints, replaced by status messages on error.

---

## 4. Screens

| Screen | Trigger |
|--------|---------|
| Bucket view | startup |
| Tag view (loading) | Enter on image |
| Tag view (loaded) | `tagStatsFetchedMsg` received |
| Confirm dialog | `d` key |
| Inspect overlay | `i` key on a tag |
| Scan results overlay | `s` key on a tag |
| Clean preview overlay | `c` key (bucket or image level) |

---

## 5. Data Flow

All S3 calls run in goroutines via `tea.Cmd` and return typed messages.

### Message types

```go
imagesFetchedMsg      { images []ImageEntry; err error }
tagsFetchedMsg        { image string; tags []TagEntry; err error }
tagStatsFetchedMsg    { tag string; stats TagStats; err error }
bucketStatsFetchedMsg { stats BucketStats; err error }
deleteResultMsg       { err error }
cleanPreviewMsg       { candidates []CleanCandidate; err error }
cleanResultMsg        { deleted int; freed int64; err error }
inspectResultMsg      { manifest string; err error }
scanResultMsg         { results string; err error }
statusClearMsg        {}
```

### Startup

1. `RootModel` created → sends `fetchImagesCmd` + `fetchBucketStatsCmd` concurrently
2. Spinner shown until both complete
3. `imagesFetchedMsg` → populates `ImageListPane`
4. `bucketStatsFetchedMsg` → populates `StatsPanel`

### Entering an image

1. Enter key → sends `fetchTagsCmd(imageName)`
2. Left pane swaps to `TagListPane` (spinner state)
3. `tagsFetchedMsg` → populates tag list
4. On first render (and on cursor move): if stats not cached → sends `fetchTagStatsCmd(tag)`
5. `tagStatsFetchedMsg` → stored in `map[string]TagStats` cache on `RootModel`; right pane re-renders

### Delete flow

1. `d` → overlay set to `ConfirmDialogModel` (no S3 call yet)
2. Confirm → `deleteCmd(target)` sent
3. `deleteResultMsg{nil}` → overlay cleared, list refreshed (re-fetch)
4. `deleteResultMsg{err}` → overlay cleared, status bar set

### Clean preview

1. `c` → `fetchCleanPreviewCmd` sent
2. `cleanPreviewMsg` → `CleanPreviewOverlay` shown
3. Confirm → `runCleanCmd` sent → `cleanResultMsg`

---

## 6. Error Handling

| Situation | Behaviour |
|-----------|-----------|
| Initial image list fetch fails | Fatal error screen (modal), `[q] quit` only |
| Tag fetch fails | Status bar: `could not load tags: <reason>` |
| Stats fetch fails | Right pane: `stats unavailable` (dim); list still navigable |
| Delete fails | Status bar: `delete failed: <reason>`; list unchanged |
| Clean preview fails | Status bar message; overlay dismissed |
| Inspect/scan fails | Status bar message |
| Context timeout | Status bar: `operation timed out`; spinners stop |

Status bar messages auto-clear after 4 seconds via a `statusClearMsg` command. No automatic retries — user presses `r` to refresh.

---

## 7. Key Bindings

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate list |
| `Enter` | Open image (bucket view) |
| `Esc` | Back to bucket view (image view) |
| `d` | Delete selected item (with confirm) |
| `i` | Inspect tag manifest |
| `s` | Scan tag for vulnerabilities |
| `c` | Clean preview |
| `r` | Refresh current view |
| `q` / `Ctrl+C` | Quit |

---

## 8. Testing

### Model unit tests (`pkg/tui/*_test.go`)

Feed messages directly into `model.Update()`, assert on returned model state:
- `imagesFetchedMsg` → list populated, spinner off
- `tagsFetchedMsg` → left pane is `TagListPane`
- `deleteResultMsg{nil}` → overlay nil, re-fetch cmd returned
- `deleteResultMsg{err}` → status bar non-empty, overlay nil
- `d` keypress → overlay is `ConfirmDialogModel`
- `Esc` keypress in image view → left pane is `ImageListPane`
- `q` keypress → `tea.Quit` cmd returned

### Cmd tests

`fetchImagesCmd`, `fetchTagsCmd`, `fetchTagStatsCmd` tested against the local filesystem backend (`pkg/storage/local`) used in existing tests.

### Not tested

- Lipgloss rendering output (brittle)
- Actual terminal interaction

### Manual smoke test (pre-merge)

1. Images load on startup; bucket stats appear in right pane
2. Enter on image → tags load; right pane updates as cursor moves
3. `d` on tag → confirm dialog; cancel leaves list unchanged; confirm deletes and refreshes
4. `i` on tag → inspect overlay shows manifest JSON
5. `s` on tag → scan results overlay
6. `c` → clean preview overlay; confirm runs clean
7. `Esc` returns to bucket view
8. `r` refreshes current view
9. `q` exits cleanly with no panic

---

## 9. Dependencies

- `github.com/charmbracelet/bubbletea` — event loop + model
- `github.com/charmbracelet/lipgloss` — styling
- `github.com/charmbracelet/bubbles/spinner` — loading indicators
- `github.com/charmbracelet/bubbles/list` — scrollable list (or custom implementation if list component is too opinionated)

All already available or easily added to `go.mod`.

---

## 10. Out of Scope

- Mouse support
- Search / filter within lists
- Multi-select delete
- Config editing via TUI
- Live auto-refresh (polling)
