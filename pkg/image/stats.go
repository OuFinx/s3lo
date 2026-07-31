package image

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/OuFinx/s3lo/v2/pkg/chunkstore"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"golang.org/x/sync/errgroup"
)

const (
	s3PricePerGBMonth  = 0.023 // US East (N. Virginia) standard
	ecrPricePerGBMonth = 0.10

	// ecrCompressionRatio converts this bucket's logical (uncompressed) bytes
	// into the number of bytes ECR would bill for.
	//
	// It is not 1. `docker save` produces uncompressed layer tars and s3lo stores
	// them verbatim, while a registry stores the gzip-compressed layer. Comparing
	// our stored bytes against ECR's price per *logical* byte would credit s3lo
	// with a saving it has not earned. Measured at 3.3-3.5x across the layers of
	// python:3.12-slim; 3.4 is the middle of that range.
	ecrCompressionRatio = 3.4
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
	Images      int `json:"images" yaml:"images"`
	Tags        int `json:"tags" yaml:"tags"`
	UniqueBlobs int `json:"unique_blobs" yaml:"unique_blobs"`
	// Chunks and ChunkBytes cover chunked layers; both are zero on a bucket that
	// has never had a chunked push. ChunkBytes is already included in BlobBytes.
	Chunks         int              `json:"chunks" yaml:"chunks"`
	ChunkBytes     int64            `json:"chunk_bytes" yaml:"chunk_bytes"`
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
//
// A ref with a trailing path scopes Images, Tags and LogicalBytes to the images
// it names. Blob and chunk totals stay bucket-wide because storage is shared:
// there is no honest way to attribute a deduplicated blob to one image.
func Stats(ctx context.Context, s3BucketRef string) (*StatsResult, error) {
	bucket, nameFilter, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	result := &StatsResult{StorageByClass: make(map[string]int64)}

	// Scan manifests for image/tag counts and logical byte totals.
	manifestKeys, err := client.ListKeys(ctx, bucket, scopedManifests(nameFilter))
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
		rel := strings.TrimPrefix(key, manifestsRoot)
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
	g.SetLimit(scanConcurrency)

	for _, key := range keys {
		key := key
		g.Go(func() error {
			data, err := client.GetObject(gCtx, bucket, key)
			if err != nil {
				return nil // skip unreadable manifests
			}

			summary, err := collectManifestSummary(gCtx, client, bucket, data)
			if err != nil {
				return nil
			}

			mu.Lock()
			logicalBytes += summary.LogicalSize
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	result.LogicalBytes = logicalBytes

	// List actual stored objects with storage class info. On a chunked bucket
	// most of the payload lives under chunks/, so counting only blobs/ would
	// report a physical size several times smaller than reality.
	blobs, err := client.ListObjectsWithMeta(ctx, bucket, "blobs/sha256/")
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

	chunks, err := client.ListObjectsWithMeta(ctx, bucket, chunkstore.ChunksPrefix)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	result.Chunks = len(chunks)
	for _, c := range chunks {
		result.ChunkBytes += c.Size
		sc := c.StorageClass
		if sc == "" {
			sc = "STANDARD"
		}
		result.StorageByClass[sc] += c.Size
	}
	result.BlobBytes += result.ChunkBytes

	// Compute cost projections.
	actualGB := float64(result.BlobBytes) / (1 << 30)
	logicalGB := float64(result.LogicalBytes) / (1 << 30)
	s3Cost := actualGB * s3PricePerGBMonth
	s3NoDedupCost := logicalGB * s3PricePerGBMonth
	ecrCost := logicalGB / ecrCompressionRatio * ecrPricePerGBMonth
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
