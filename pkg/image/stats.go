package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	storage "github.com/OuFinx/s3lo/pkg/storage"
	"golang.org/x/sync/errgroup"
)

const (
	s3PricePerGBMonth  = 0.023 // US East (N. Virginia) standard
	ecrPricePerGBMonth = 0.10
)

// CostEstimate holds projected monthly cost figures for a bucket.
type CostEstimate struct {
	S3Monthly        float64 `json:"s3_monthly" yaml:"s3_monthly"`
	S3NoDedupMonthly float64 `json:"s3_no_dedup_monthly" yaml:"s3_no_dedup_monthly"`
	ECRMonthly       float64 `json:"ecr_monthly" yaml:"ecr_monthly"`
	SavingsVsECR     float64 `json:"savings_vs_ecr" yaml:"savings_vs_ecr"`
	SavingsPct       float64 `json:"savings_pct" yaml:"savings_pct"`
}

// StatsResult holds storage statistics for a bucket.
type StatsResult struct {
	Images         int              `json:"images" yaml:"images"`
	Tags           int              `json:"tags" yaml:"tags"`
	UniqueBlobs    int              `json:"unique_blobs" yaml:"unique_blobs"`
	BlobBytes      int64            `json:"blob_bytes" yaml:"blob_bytes"`
	LogicalBytes   int64            `json:"logical_bytes" yaml:"logical_bytes"`
	StorageByClass map[string]int64 `json:"storage_by_class" yaml:"storage_by_class"`
	Cost           CostEstimate     `json:"cost" yaml:"cost"`
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

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	result := &StatsResult{StorageByClass: make(map[string]int64)}

	// Scan manifests for image/tag counts and logical byte totals.
	manifestsPrefix := prefix + "manifests/"
	manifestKeys, err := client.ListKeys(ctx, bucket, manifestsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	// Filter to manifest.json keys only.
	var keys []string
	seenImages := make(map[string]bool)
	for _, key := range manifestKeys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
		keys = append(keys, key)

		// Extract image name from: manifests/<image...>/<tag>/manifest.json
		rel := strings.TrimPrefix(key, manifestsPrefix)
		rel = strings.TrimSuffix(rel, "/manifest.json")
		if lastSlash := strings.LastIndex(rel, "/"); lastSlash >= 0 {
			seenImages[rel[:lastSlash]] = true
		}
	}

	result.Tags = len(keys)
	result.Images = len(seenImages)

	// Fetch all manifests in parallel to compute logical byte totals.
	var (
		mu           sync.Mutex
		logicalBytes int64
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(20)

	for _, key := range keys {
		key := key
		g.Go(func() error {
			data, err := client.GetObject(gCtx, bucket, key)
			if err != nil {
				return nil // skip unreadable manifests
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
				return nil
			}

			total := m.Config.Size
			for _, layer := range m.Layers {
				total += layer.Size
			}

			mu.Lock()
			logicalBytes += total
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	result.LogicalBytes = logicalBytes

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

	// Compute cost projections.
	actualGB := float64(result.BlobBytes) / (1 << 30)
	logicalGB := float64(result.LogicalBytes) / (1 << 30)
	s3Cost := actualGB * s3PricePerGBMonth
	s3NoDedupCost := logicalGB * s3PricePerGBMonth
	ecrCost := logicalGB * ecrPricePerGBMonth
	savingsVsECR := ecrCost - s3Cost
	savingsPct := 0.0
	if ecrCost > 0 {
		savingsPct = savingsVsECR / ecrCost * 100
	}
	result.Cost = CostEstimate{
		S3Monthly:        s3Cost,
		S3NoDedupMonthly: s3NoDedupCost,
		ECRMonthly:       ecrCost,
		SavingsVsECR:     savingsVsECR,
		SavingsPct:       savingsPct,
	}

	return result, nil
}
