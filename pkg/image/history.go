package image

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// HistoryEntry records a single push event for an image tag.
type HistoryEntry struct {
	PushedAt  time.Time `json:"pushed_at"`
	Digest    string    `json:"digest"`
	SizeBytes int64     `json:"size_bytes"`
}

// ImageHistorySummary is the Mode A (bucket-level) output: one row per image.
type ImageHistorySummary struct {
	Name           string    `json:"name" yaml:"name"`
	Tags           int       `json:"tags" yaml:"tags"`
	LastPushedAt   time.Time `json:"last_pushed_at" yaml:"last_pushed_at"`
	TotalSizeBytes int64     `json:"total_size_bytes" yaml:"total_size_bytes"`
}

// TagHistoryEntry is the Mode B (repository-level) output: one row per push across all tags.
// Superseded is true for older pushes of the same tag that have been overwritten.
type TagHistoryEntry struct {
	Tag        string    `json:"tag" yaml:"tag"`
	PushedAt   time.Time `json:"pushed_at" yaml:"pushed_at"`
	Digest     string    `json:"digest" yaml:"digest"`
	SizeBytes  int64     `json:"size_bytes" yaml:"size_bytes"`
	Superseded bool      `json:"superseded,omitempty" yaml:"superseded,omitempty"`
}

// ListImageHistory returns push-history summaries for every image in the bucket (Mode A).
// Scans all manifests/<image>/<tag>/history.json, groups by image.
func ListImageHistory(ctx context.Context, bucketRef string) ([]ImageHistorySummary, error) {
	bucket, prefix, err := ParseBucketRef(bucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, bucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	manifestsPrefix := prefix + "manifests/"
	keys, err := client.ListKeys(ctx, bucket, manifestsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	type imageAcc struct {
		tags       map[string]struct{}
		lastPushed time.Time
		totalSize  int64
	}
	acc := make(map[string]*imageAcc)

	for _, key := range keys {
		if !strings.HasSuffix(key, "/history.json") {
			continue
		}
		// key = <prefix>manifests/<image>/<tag>/history.json
		rel := strings.TrimPrefix(key, manifestsPrefix)
		rel = strings.TrimSuffix(rel, "/history.json")
		lastSlash := strings.LastIndex(rel, "/")
		if lastSlash < 0 {
			continue
		}
		imgName := rel[:lastSlash]
		tagName := rel[lastSlash+1:]

		data, err := client.GetObject(ctx, bucket, key)
		if err != nil {
			continue
		}
		var entries []HistoryEntry
		if err := json.Unmarshal(data, &entries); err != nil || len(entries) == 0 {
			continue
		}

		a, ok := acc[imgName]
		if !ok {
			a = &imageAcc{tags: make(map[string]struct{})}
			acc[imgName] = a
		}
		a.tags[tagName] = struct{}{}
		// Most recent push for this tag (entries are newest-first).
		if entries[0].PushedAt.After(a.lastPushed) {
			a.lastPushed = entries[0].PushedAt
		}
		a.totalSize += entries[0].SizeBytes
	}

	result := make([]ImageHistorySummary, 0, len(acc))
	for name, a := range acc {
		result = append(result, ImageHistorySummary{
			Name:           name,
			Tags:           len(a.tags),
			LastPushedAt:   a.lastPushed,
			TotalSizeBytes: a.totalSize,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastPushedAt.After(result[j].LastPushedAt)
	})
	return result, nil
}

// ListTagHistory returns push history for all tags of a single image (Mode B).
// rawRef may include the image name (e.g. "local://./local-s3/alpine") —
// imageName is extracted separately by the caller via ParseConfigRef.
// Scans manifests/<imageName>/*/history.json, merges and sorts newest-first.
func ListTagHistory(ctx context.Context, rawRef, imageName string) ([]TagHistoryEntry, error) {
	bucket, _, err := ParseConfigRef(rawRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, rawRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	imagePrefix := "manifests/" + imageName + "/"
	keys, err := client.ListKeys(ctx, bucket, imagePrefix)
	if err != nil {
		return nil, fmt.Errorf("list tag manifests: %w", err)
	}

	var result []TagHistoryEntry
	for _, key := range keys {
		if !strings.HasSuffix(key, "/history.json") {
			continue
		}
		rel := strings.TrimPrefix(key, imagePrefix)
		tagName := strings.TrimSuffix(rel, "/history.json")
		if strings.Contains(tagName, "/") {
			continue
		}

		data, err := client.GetObject(ctx, bucket, key)
		if err != nil {
			continue
		}
		var entries []HistoryEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			continue
		}
		for i, e := range entries {
			result = append(result, TagHistoryEntry{
				Tag:        tagName,
				PushedAt:   e.PushedAt,
				Digest:     e.Digest,
				SizeBytes:  e.SizeBytes,
				Superseded: i > 0, // entries[0] is the current version; the rest are overridden
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].PushedAt.After(result[j].PushedAt)
	})
	return result, nil
}

func manifestLogicalSize(ctx context.Context, client storage.Backend, bucket string, manifestData []byte) int64 {
	summary, err := collectManifestSummary(ctx, client, bucket, manifestData)
	if err != nil {
		return 0
	}
	return summary.LogicalSize
}

// readHistory reads history.json for the given image tag.
func readHistory(ctx context.Context, client storage.Backend, parsed ref.Reference) ([]HistoryEntry, error) {
	key := parsed.ManifestsPrefix() + "history.json"
	data, err := client.GetObject(ctx, parsed.Bucket, key)
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, nil // no history yet
		}
		return nil, fmt.Errorf("read history: %w", err)
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse history: %w", err)
	}
	return entries, nil
}

// recordHistory prepends a new push event to the tag's history.json.
// Called from Push after a successful upload.
func recordHistory(ctx context.Context, client storage.Backend, parsed ref.Reference, manifestData []byte, sizeBytes int64) error {
	h := sha256.Sum256(manifestData)
	entry := HistoryEntry{
		PushedAt:  time.Now().UTC().Truncate(time.Second),
		Digest:    fmt.Sprintf("sha256:%x", h),
		SizeBytes: sizeBytes,
	}

	// Read existing history, prepend new entry (keep newest first).
	entries, _ := readHistory(ctx, client, parsed) // ignore read errors; start fresh
	entries = append([]HistoryEntry{entry}, entries...)

	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	key := parsed.ManifestsPrefix() + "history.json"
	return client.PutObject(ctx, parsed.Bucket, key, data)
}
