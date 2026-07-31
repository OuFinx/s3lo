package image

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/OuFinx/s3lo/v2/pkg/oci"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// SharedLayer is one unique layer and the tags that reference it.
type SharedLayer struct {
	Digest string   `json:"digest" yaml:"digest"`
	Size   int64    `json:"size" yaml:"size"`
	Tags   []string `json:"tags" yaml:"tags"`
}

// LayerSharingResult reports which layers are shared across which tags.
type LayerSharingResult struct {
	Tags   []string      `json:"tags" yaml:"tags"`
	Layers []SharedLayer `json:"layers" yaml:"layers"`
	// StoredBytes counts every unique layer once; LogicalBytes counts it once per
	// tag that references it, so the gap between them is what dedup saved.
	StoredBytes  int64 `json:"stored_bytes" yaml:"stored_bytes"`
	LogicalBytes int64 `json:"logical_bytes" yaml:"logical_bytes"`
}

// DedupPercent returns the percentage of layer bytes saved by sharing.
func (r *LayerSharingResult) DedupPercent() float64 {
	if r.LogicalBytes == 0 {
		return 0
	}
	return float64(r.LogicalBytes-r.StoredBytes) / float64(r.LogicalBytes) * 100
}

// LayerSharing collects every tag's layer set and reports which layers are
// shared across which tags.
func LayerSharing(ctx context.Context, s3BucketRef string) (*LayerSharingResult, error) {
	bucket, nameFilter, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	manifestKeys, err := client.ListKeys(ctx, bucket, scopedManifests(nameFilter))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	type tagRef struct {
		name string // "<image>:<tag>"
		key  string
	}
	var tags []tagRef
	for _, key := range manifestKeys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
		// key = manifests/<image...>/<tag>/manifest.json
		rel := strings.TrimSuffix(strings.TrimPrefix(key, manifestsRoot), "/manifest.json")
		lastSlash := strings.LastIndex(rel, "/")
		if lastSlash < 0 {
			continue
		}
		tags = append(tags, tagRef{name: rel[:lastSlash] + ":" + rel[lastSlash+1:], key: key})
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].name < tags[j].name })

	result := &LayerSharingResult{Tags: make([]string, len(tags))}
	for i, t := range tags {
		result.Tags[i] = t.name
	}

	perTag := make([]map[string]int64, len(tags))
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(scanConcurrency)
	for i, t := range tags {
		i, t := i, t
		g.Go(func() error {
			data, err := client.GetObject(gCtx, bucket, t.key)
			if err != nil {
				return nil // skip unreadable manifests, same as Stats
			}
			perTag[i] = collectLayerSizes(gCtx, client, bucket, data)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	byDigest := make(map[string]*SharedLayer)
	for i, layers := range perTag {
		for digest, size := range layers {
			row, ok := byDigest[digest]
			if !ok {
				row = &SharedLayer{Digest: digest, Size: size}
				byDigest[digest] = row
			}
			row.Tags = append(row.Tags, tags[i].name)
		}
	}

	result.Layers = make([]SharedLayer, 0, len(byDigest))
	for _, row := range byDigest {
		sort.Strings(row.Tags)
		result.Layers = append(result.Layers, *row)
		result.StoredBytes += row.Size
		result.LogicalBytes += row.Size * int64(len(row.Tags))
	}

	// Most-shared first, then largest, then digest so the order is stable.
	sort.Slice(result.Layers, func(i, j int) bool {
		a, b := result.Layers[i], result.Layers[j]
		if len(a.Tags) != len(b.Tags) {
			return len(a.Tags) > len(b.Tags)
		}
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return a.Digest < b.Digest
	})

	return result, nil
}

// collectLayerSizes returns digest -> size for every layer a manifest document
// references. Image indexes are resolved to their platform manifests, whose
// layers are merged (a layer shared by amd64 and arm64 is counted once).
// Unreadable or unparseable children are skipped rather than failing the scan.
func collectLayerSizes(ctx context.Context, client storage.Backend, bucket string, data []byte) map[string]int64 {
	sizes := make(map[string]int64)
	visited := make(map[string]struct{})

	var mu sync.Mutex
	var walk func([]byte)
	walk = func(d []byte) {
		if isImageIndex(d) {
			idx, err := parseIndex(d)
			if err != nil {
				return
			}
			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(scanConcurrency)
			for _, desc := range idx.Manifests {
				digest := desc.Digest.Encoded()
				// Skip cosign/SLSA attestation pseudo-manifests, but keep entries
				// with no platform at all — those are ordinary manifests.
				if digest == "" || (desc.Platform != nil && IsAttestationPlatform(platformString(desc.Platform))) {
					continue
				}
				mu.Lock()
				_, seen := visited[digest]
				visited[digest] = struct{}{}
				mu.Unlock()
				if seen {
					continue
				}
				g.Go(func() error {
					childData, err := client.GetObject(gCtx, bucket, "blobs/sha256/"+digest)
					if err != nil {
						return nil
					}
					walk(childData)
					return nil
				})
			}
			_ = g.Wait()
			return
		}

		manifest, err := oci.ParseManifest(d)
		if err != nil {
			return
		}
		mu.Lock()
		for _, layer := range manifest.Layers {
			if digest := layer.Digest.Encoded(); digest != "" {
				sizes[digest] = layer.Size
			}
		}
		mu.Unlock()
	}

	walk(data)
	return sizes
}
