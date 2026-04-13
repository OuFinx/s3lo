package image

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// ImageEntry represents an image and its available tags in the registry.
type ImageEntry struct {
	Name string   `json:"name" yaml:"name"`
	Tags []string `json:"tags" yaml:"tags"`
}

// List lists all images in a storage path.
// Supports both v1.1.0 (manifests/ prefix) and v1.0.0 (per-tag root) layouts.
// s3Ref should be like "s3://my-bucket/" or "local:///path/to/store/".
func List(ctx context.Context, s3Ref string) ([]ImageEntry, error) {
	bucket, prefix, err := ParseBucketRef(s3Ref)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	slog.Debug("listing images", "bucket", bucket, "prefix", prefix)

	// v1.1.0: scan manifests/<image>/<tag>/manifest.json
	manifestKeys, err := client.ListKeys(ctx, bucket, prefix+"manifests/")
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	slog.Debug("found manifest keys", "count", len(manifestKeys))

	imageMap := make(map[string][]string) // image → tags
	manifestsPrefix := prefix + "manifests/"
	for _, key := range manifestKeys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
		// key = manifests/<image>/<tag>/manifest.json
		rel := strings.TrimPrefix(key, manifestsPrefix)
		rel = strings.TrimSuffix(rel, "/manifest.json")
		// rel = <image>/<tag>
		lastSlash := strings.LastIndex(rel, "/")
		if lastSlash < 0 {
			continue
		}
		imgName := rel[:lastSlash]
		tag := rel[lastSlash+1:]
		imageMap[imgName] = append(imageMap[imgName], tag)
	}

	var entries []ImageEntry
	seen := make(map[string]bool)
	for name, tags := range imageMap {
		seen[name] = true
		entries = append(entries, ImageEntry{Name: name, Tags: tags})
	}

	// v1.0.0 fallback: scan <image>/<tag>/manifest.json at root
	allKeys, err := client.ListKeys(ctx, bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("list root: %w", err)
	}
	v100Map := make(map[string][]string)
	for _, key := range allKeys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
		rel := strings.TrimPrefix(key, prefix)
		// Skip v1.1.0 prefixes
		if strings.HasPrefix(rel, "blobs/") || strings.HasPrefix(rel, "manifests/") {
			continue
		}
		rel = strings.TrimSuffix(rel, "/manifest.json")
		// rel = <image>/<tag>
		lastSlash := strings.LastIndex(rel, "/")
		if lastSlash < 0 {
			continue
		}
		imgName := rel[:lastSlash]
		tag := rel[lastSlash+1:]
		if !seen[imgName] {
			v100Map[imgName] = append(v100Map[imgName], tag)
		}
	}
	for name, tags := range v100Map {
		entries = append(entries, ImageEntry{Name: name, Tags: tags})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	for i := range entries {
		sort.Strings(entries[i].Tags)
	}

	return entries, nil
}
