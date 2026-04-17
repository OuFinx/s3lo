package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sync/errgroup"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/OuFinx/s3lo/pkg/oci"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

const tuiS3PricePerGBMonth = 0.023

// fetchImagesCmd lists images with per-image total sizes.
func fetchImagesCmd(ctx context.Context, st storage.Backend, bucket, prefix string) tea.Cmd {
	return func() tea.Msg {
		manifestsPrefix := prefix + "manifests/"
		objects, err := st.ListObjectsWithMeta(ctx, bucket, manifestsPrefix)
		if err != nil {
			return imagesFetchedMsg{err: fmt.Errorf("list images: %w", err)}
		}

		type imgAccum struct {
			tags  map[string]bool
			bytes int64
		}
		accum := make(map[string]*imgAccum)
		var mu sync.Mutex

		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(20)

		for _, obj := range objects {
			if !strings.HasSuffix(obj.Key, "/manifest.json") {
				continue
			}
			obj := obj
			rel := strings.TrimPrefix(obj.Key, manifestsPrefix)
			rel = strings.TrimSuffix(rel, "/manifest.json")
			lastSlash := strings.LastIndex(rel, "/")
			if lastSlash < 0 {
				continue
			}
			imgName := rel[:lastSlash]
			tagName := rel[lastSlash+1:]

			mu.Lock()
			if accum[imgName] == nil {
				accum[imgName] = &imgAccum{tags: make(map[string]bool)}
			}
			accum[imgName].tags[tagName] = true
			mu.Unlock()

			g.Go(func() error {
				data, err := st.GetObject(gCtx, bucket, obj.Key)
				if err != nil {
					return nil // skip unreadable manifests
				}
				size := sumManifestBytes(data)
				mu.Lock()
				if a := accum[imgName]; a != nil {
					a.bytes += size
				}
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return imagesFetchedMsg{err: err}
		}

		entries := make([]ImageListEntry, 0, len(accum))
		for name, a := range accum {
			entries = append(entries, ImageListEntry{
				Name:       name,
				TagCount:   len(a.tags),
				TotalBytes: a.bytes,
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name < entries[j].Name
		})
		return imagesFetchedMsg{entries: entries}
	}
}

// fetchBucketStatsCmd collects bucket-level statistics via image.Stats.
func fetchBucketStatsCmd(ctx context.Context, s3Ref string) tea.Cmd {
	return func() tea.Msg {
		sr, err := image.Stats(ctx, s3Ref)
		if err != nil {
			return bucketStatsFetchedMsg{err: err}
		}
		return bucketStatsFetchedMsg{stats: BucketStats{
			Images:       sr.Images,
			Tags:         sr.Tags,
			UniqueBlobs:  sr.UniqueBlobs,
			TotalBytes:   sr.BlobBytes,
			LogicalBytes: sr.LogicalBytes,
			CostMonthly:  sr.Cost.S3Monthly,
			ECRMonthly:   sr.Cost.ECRMonthly,
			SavingsPct:   sr.Cost.SavingsPct,
		}}
	}
}

// fetchTagsCmd lists tags for an image sorted newest-first, with per-tag sizes.
func fetchTagsCmd(ctx context.Context, st storage.Backend, bucket, prefix, imageName string) tea.Cmd {
	return func() tea.Msg {
		tagPrefix := prefix + "manifests/" + imageName + "/"
		objects, err := st.ListObjectsWithMeta(ctx, bucket, tagPrefix)
		if err != nil {
			return tagsFetchedMsg{imageName: imageName, err: fmt.Errorf("list tags: %w", err)}
		}

		type tagAccum struct {
			lastModified time.Time
			bytes        int64
		}
		accum := make(map[string]*tagAccum)
		var mu sync.Mutex

		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(20)

		for _, obj := range objects {
			if !strings.HasSuffix(obj.Key, "/manifest.json") {
				continue
			}
			rel := strings.TrimPrefix(obj.Key, tagPrefix)
			tagName := strings.TrimSuffix(rel, "/manifest.json")
			if tagName == "" || strings.Contains(tagName, "/") {
				continue
			}
			obj, tagName := obj, tagName
			mu.Lock()
			if a := accum[tagName]; a == nil || obj.LastModified.After(a.lastModified) {
				accum[tagName] = &tagAccum{lastModified: obj.LastModified}
			}
			mu.Unlock()

			g.Go(func() error {
				data, err := st.GetObject(gCtx, bucket, obj.Key)
				if err != nil {
					return nil // skip unreadable manifests
				}
				size := sumManifestBytes(data)
				mu.Lock()
				if a := accum[tagName]; a != nil {
					a.bytes = size
				}
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return tagsFetchedMsg{imageName: imageName, err: err}
		}

		tags := make([]TagEntry, 0, len(accum))
		for name, a := range accum {
			tags = append(tags, TagEntry{Name: name, LastModified: a.lastModified, TotalBytes: a.bytes})
		}
		sort.Slice(tags, func(i, j int) bool {
			return tags[i].LastModified.After(tags[j].LastModified)
		})
		return tagsFetchedMsg{imageName: imageName, tags: tags}
	}
}

// fetchTagStatsCmd loads detailed stats for one tag via image.Inspect.
func fetchTagStatsCmd(ctx context.Context, s3Ref, imageName, tagName string) tea.Cmd {
	return func() tea.Msg {
		tagRef := strings.TrimSuffix(s3Ref, "/") + "/" + imageName + ":" + tagName
		cacheKey := imageName + ":" + tagName
		info, err := image.Inspect(ctx, tagRef)
		if err != nil {
			return tagStatsFetchedMsg{cacheKey: cacheKey, err: err}
		}
		stats := TagStats{
			Signed:     len(info.Signatures) > 0,
			LayerCount: len(info.Layers),
			TotalBytes: info.TotalSize,
		}
		if info.IsIndex {
			stats.TotalBytes = 0
			stats.LayerCount = 0
			for _, p := range info.Platforms {
				if !image.IsAttestationPlatform(p.Platform) {
					stats.Platforms = append(stats.Platforms, p.Platform)
					stats.TotalBytes += p.TotalSize
					stats.LayerCount += len(p.Layers)
				}
			}
		}
		stats.CostMonthly = float64(stats.TotalBytes) / (1 << 30) * tuiS3PricePerGBMonth
		return tagStatsFetchedMsg{cacheKey: cacheKey, stats: stats}
	}
}

// deleteTagCmd deletes a tag from S3.
func deleteTagCmd(ctx context.Context, tagRef string) tea.Cmd {
	return func() tea.Msg {
		return deleteResultMsg{err: image.Delete(ctx, tagRef)}
	}
}

// inspectTagCmd fetches image metadata and formats it as pretty-printed JSON.
func inspectTagCmd(ctx context.Context, tagRef string) tea.Cmd {
	return func() tea.Msg {
		info, err := image.Inspect(ctx, tagRef)
		if err != nil {
			return inspectResultMsg{err: err}
		}
		content, err := info.FormatJSON()
		if err != nil {
			return inspectResultMsg{err: err}
		}
		return inspectResultMsg{content: content}
	}
}

// prepareScanCmd downloads the image to a temp OCI layout directory for Trivy.
func prepareScanCmd(ctx context.Context, tagRef string) tea.Cmd {
	return func() tea.Msg {
		trivyPath, tmpDir, err := image.PullToOCILayout(ctx, tagRef, image.ScanOptions{})
		if err != nil {
			return scanPreparedMsg{err: err}
		}
		return scanPreparedMsg{tmpDir: tmpDir, trivyPath: trivyPath}
	}
}

// fetchCleanPreviewCmd dry-runs lifecycle + GC and returns a summary.
func fetchCleanPreviewCmd(ctx context.Context, st storage.Backend, bucket, s3Ref string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := image.GetBucketConfig(ctx, st, bucket)
		if err != nil {
			cfg = &image.BucketConfig{}
		}
		lcResult, lcErr := image.ApplyLifecycle(ctx, s3Ref, cfg, true)
		gcResult, gcErr := image.GC(ctx, s3Ref, true)
		if lcErr != nil && gcErr != nil {
			return cleanPreviewFetchedMsg{err: fmt.Errorf("lifecycle: %v; gc: %v", lcErr, gcErr)}
		}
		preview := CleanPreview{}
		if lcErr == nil && lcResult != nil {
			preview.TagsPruned = lcResult.Deleted
		}
		if gcErr == nil && gcResult != nil {
			preview.BlobsFreed = gcResult.Deleted
			preview.FreedBytes = gcResult.FreedBytes
		}
		return cleanPreviewFetchedMsg{preview: preview}
	}
}

// runCleanCmd runs lifecycle + GC for real.
func runCleanCmd(ctx context.Context, st storage.Backend, bucket, s3Ref string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := image.GetBucketConfig(ctx, st, bucket)
		if err != nil {
			cfg = &image.BucketConfig{}
		}
		lcResult, _ := image.ApplyLifecycle(ctx, s3Ref, cfg, false)
		gcResult, gcErr := image.GC(ctx, s3Ref, false)
		if gcErr != nil {
			return cleanResultMsg{err: gcErr}
		}
		deleted := gcResult.Deleted
		if lcResult != nil {
			deleted += lcResult.Deleted
		}
		return cleanResultMsg{deleted: deleted, freed: gcResult.FreedBytes}
	}
}

// clearStatusCmd sends statusClearMsg after 4 seconds.
func clearStatusCmd() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return statusClearMsg{}
	})
}

// sumManifestBytes parses manifest JSON and returns the sum of config + layer sizes.
// Returns 0 for image indexes (they have no direct layers).
func sumManifestBytes(data []byte) int64 {
	m, err := oci.ParseManifest(data)
	if err != nil {
		return 0
	}
	var total int64
	total += m.Config.Size
	for _, layer := range m.Layers {
		total += layer.Size
	}
	return total
}
