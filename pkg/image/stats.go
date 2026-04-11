package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// StatsResult holds storage statistics for a bucket.
type StatsResult struct {
	Images         int
	Tags           int
	UniqueBlobs    int
	BlobBytes      int64            // actual bytes stored in blobs/sha256/
	LogicalBytes   int64            // bytes referenced by manifests (pre-dedup total)
	StorageByClass map[string]int64 // storage class -> bytes
}

// DedupSavings returns bytes saved by cross-image blob deduplication.
func (s *StatsResult) DedupSavings() int64 {
	if s.LogicalBytes > s.BlobBytes {
		return s.LogicalBytes - s.BlobBytes
	}
	return 0
}

// DedupPercent returns the percentage of space saved by deduplication.
func (s *StatsResult) DedupPercent() float64 {
	if s.LogicalBytes == 0 {
		return 0
	}
	return float64(s.DedupSavings()) / float64(s.LogicalBytes) * 100
}

// Stats collects storage statistics for a bucket.
func Stats(ctx context.Context, s3BucketRef string) (*StatsResult, error) {
	bucket, prefix, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	result := &StatsResult{StorageByClass: make(map[string]int64)}

	// Scan manifests for image/tag counts and logical byte totals.
	manifestsPrefix := prefix + "manifests/"
	manifestKeys, err := client.ListKeys(ctx, bucket, manifestsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	seenImages := make(map[string]bool)
	for _, key := range manifestKeys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}

		result.Tags++

		// Extract image name from: manifests/<image...>/<tag>/manifest.json
		rel := strings.TrimPrefix(key, manifestsPrefix)
		rel = strings.TrimSuffix(rel, "/manifest.json")
		if lastSlash := strings.LastIndex(rel, "/"); lastSlash >= 0 {
			seenImages[rel[:lastSlash]] = true
		}

		data, err := client.GetObject(ctx, bucket, key)
		if err != nil {
			continue // skip unreadable manifests
		}

		var m struct {
			Config struct {
				Size int64 `json:"size"`
			} `json:"config"`
			Layers []struct {
				Size int64 `json:"size"`
			} `json:"layers"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}

		result.LogicalBytes += m.Config.Size
		for _, layer := range m.Layers {
			result.LogicalBytes += layer.Size
		}
	}
	result.Images = len(seenImages)

	// List actual stored blobs with storage class info.
	blobsPrefix := prefix + "blobs/sha256/"
	blobs, err := client.ListObjectsWithMeta(ctx, bucket, blobsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}

	result.UniqueBlobs = len(blobs)
	for _, blob := range blobs {
		result.BlobBytes += blob.Size
		sc := blob.StorageClass
		if sc == "" {
			sc = "STANDARD"
		}
		result.StorageByClass[sc] += blob.Size
	}

	return result, nil
}
