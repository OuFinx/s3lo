# tui

Interactive terminal UI for browsing images, inspecting tags, and managing lifecycle — all without leaving the terminal.

```
s3lo tui <s3-bucket-ref>
```

## Examples

```bash
s3lo tui s3://my-bucket/
s3lo tui s3://my-bucket/prefix/
s3lo tui gs://my-gcs-bucket/
s3lo tui az://my-container/
s3lo tui local://./local-s3/
```

## Layout

```
 myapp          4 tags   120.5 MB │ Image: myapp
 nginx          1 tag     50.2 MB │ Tags:       4
                                  │ Blobs:      12
                                  │ Stored:     120.5 MB
                                  │ Logical:    280.1 MB
                                  │ Dedup:      57% saved
                                  │
                                  │ Est. cost: $0.00/month
                                  │ ECR equiv: $0.01/month
                                  │ You save:  $0.01/month (57%)
──────────────────────────────────┴──────────────────────────────
  [↑↓] navigate  [enter] open  [d] delete  [c] clean  [r] refresh  [q] quit
```

The UI has two panels:

- **Left pane** — image list or tag list, depending on navigation depth
- **Right pane** — storage stats and cost estimate for the selected image or tag

The status bar is always pinned to the bottom line.

## Navigation

### Image list (top level)

| Key | Action |
|-----|--------|
| `↑` / `↓` or `k` / `j` | Move cursor |
| `enter` | Open tag list for selected image |
| `d` | Delete all tags for selected image (with confirmation) |
| `c` | Open clean preview (lifecycle + GC dry run) |
| `r` | Refresh |
| `q` / `ctrl+c` | Quit |

### Tag list (inside an image)

| Key | Action |
|-----|--------|
| `↑` / `↓` or `k` / `j` | Move cursor; right panel updates to selected tag stats |
| `esc` | Return to image list |
| `d` | Delete selected tag (with confirmation) |
| `i` | Inspect selected tag — shows metadata as formatted JSON |
| `s` | Scan selected tag for vulnerabilities with Trivy |
| `g` | Open layer sharing matrix for all tags of this image |
| `c` | Open clean preview |
| `r` | Refresh |
| `q` / `ctrl+c` | Quit |

## Right panel

At the image level, the right panel shows aggregate stats for all tags of the selected image.

At the tag level, the right panel shows:

- Total size (resolved across platforms for multi-arch images)
- Layer count
- Platform list (for multi-arch images)
- Signed status
- Estimated monthly S3 cost

Cost rows are hidden when using `local://` backends.

## Layer sharing matrix (`g`)

Press `g` while in the tag list to open the layer sharing matrix overlay. It shows which layers are shared across all tags of the image.

```
Layer sharing — myapp

  Layer                 Size      v1.0    v1.1    v2.0
  sha256:aabbccdd1234…  50.0 MB   ████    ████    ████   ← 3 tags
  sha256:eeff99887766…  20.0 MB   ████    ████    ····   ← 2 tags
  sha256:11223344aabb…  10.0 MB   ████    ····    ····
  sha256:55667788ccdd…  15.0 MB   ····    ····    ████

  4 unique layers · 95.0 MB stored · 175.0 MB logical · 46% dedup

  [↑↓] scroll layers  [←→] scroll tags  [esc] close
```

Rows are sorted by share count (most-shared first), then by size. Shared layers are annotated with the number of tags that include them.

Use `↑` / `↓` to scroll layer rows and `←` / `→` to scroll tag columns when there are more than 14 rows or 6 columns.

Press `esc`, `q`, or `g` again to close the overlay.

## Inspect overlay (`i`)

Press `i` on a tag to view its full metadata as pretty-printed JSON: layers, digests, sizes, platforms (for multi-arch images), and signatures.

Press `esc` or `q` to close.

## Scan overlay (`s`)

Press `s` on a tag to scan it for vulnerabilities using Trivy. s3lo downloads the image to a temporary OCI layout directory and passes it to Trivy, which opens in the terminal.

Press `esc` to cancel before the scan starts.

## Clean preview (`c`)

Press `c` from anywhere to open the clean preview overlay. It shows what a dry-run of `s3lo clean` would do: tags to prune (based on lifecycle rules) and unreferenced blobs to free. Confirm in the overlay to run the actual clean.

## Delete confirmation (`d`)

Pressing `d` opens a confirmation dialog before any deletion. Press `esc` to cancel without deleting.

## Notes

- The TUI uses the [Bubbletea](https://github.com/charmbracelet/bubbletea) framework and requires a terminal that supports ANSI escape codes.
- All data is fetched from the bucket on demand — no local cache beyond the current session.
- The layer matrix is built by reading all tag manifests in parallel. For buckets with many tags, the first load may take a few seconds.
