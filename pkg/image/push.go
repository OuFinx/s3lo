package image

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/OuFinx/s3lo/pkg/chunkstore"
	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// tagManifestFile is the single object that constitutes a tag.
//
// A tag used to be four objects (manifest.json, config.json, index.json,
// oci-layout) written in a loop, which made every tag update non-atomic: a
// reader could observe a new manifest against a stale config, and a push that
// died halfway left the tag permanently inconsistent. Nothing ever read the
// other three — every reader in this package resolves a tag through
// manifest.json and then fetches blobs by digest — and the per-tag directory was
// not a valid OCI layout anyway, because blobs/ lives at the bucket root rather
// than beside it. Collapsing a tag to one object makes the write atomic by
// construction: S3 PutObject either lands or it does not.
const tagManifestFile = "manifest.json"

// PushOptions controls push behavior.
type PushOptions struct {
	// Force overwrites an existing tag even if the bucket has immutability enabled.
	Force bool
	// OnStart is called once with the total blob bytes before any uploads begin.
	OnStart func(totalBytes int64)
	// OnBlob is called for each blob after it is processed (uploaded or skipped).
	// digest is the sha256 digest (without "sha256:" prefix), size in bytes, skipped=true if already existed.
	OnBlob func(digest string, size int64, skipped bool)
	// OnTransfer is called once after all blobs are stored, with how much of the
	// image had to be uploaded versus reused. On a chunked bucket this is where
	// sub-layer deduplication becomes visible.
	OnTransfer func(stats chunkstore.Stats)
}

// Push exports a local Docker image and uploads it to S3 using the v1.1.0 layout:
//   - blobs -> blobs/sha256/<digest>  (global, Intelligent-Tiering, cross-image dedup)
//   - manifests -> manifests/<image>/<tag>/  (Standard storage class)
func Push(ctx context.Context, imageRef, s3Ref string, opts PushOptions) error {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid S3 reference: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "s3lo-push-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	_, manifestData, configData, err := oci.ExportImage(ctx, imageRef, tmpDir)
	if err != nil {
		return fmt.Errorf("export image: %w", err)
	}

	if err := oci.WriteOCILayout(tmpDir, manifestData, configData); err != nil {
		return fmt.Errorf("write OCI layout: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	if err := enforceTagWritePolicy(ctx, client, parsed, opts.Force); err != nil {
		return err
	}

	// Upload blobs to global blobs/sha256/ with Intelligent-Tiering in parallel.
	blobsDir := filepath.Join(tmpDir, "blobs", "sha256")
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		return fmt.Errorf("read blobs dir: %w", err)
	}

	// Sum blob sizes for deterministic progress reporting.
	if opts.OnStart != nil {
		var totalBytes int64
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if info, err := entry.Info(); err == nil {
				totalBytes += info.Size()
			}
		}
		if totalBytes > 0 {
			opts.OnStart(totalBytes)
		}
	}

	// Chunking is a property of the bucket, read once per push.
	cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
	if err != nil {
		return fmt.Errorf("read bucket config: %w", err)
	}
	chunked := cfg.ChunkedEnabled()

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(blobConcurrency)
	var (
		onBlobMu sync.Mutex
		total    chunkstore.Stats
	)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entry := entry
		g.Go(func() error {
			localPath := filepath.Join(blobsDir, entry.Name())

			info, err := os.Stat(localPath)
			if err != nil {
				return fmt.Errorf("stat blob %s: %w", entry.Name(), err)
			}

			stats, skipped, err := storeLayer(gCtx, client, parsed.Bucket, localPath,
				entry.Name(), info.Size(), chunked)
			if err != nil {
				return err
			}
			slog.Debug("stored blob", "digest", entry.Name()[:12], "size", info.Size(),
				"chunks", stats.Chunks, "uploaded", stats.BytesUploaded, "skipped", skipped)

			onBlobMu.Lock()
			total.Chunks += stats.Chunks
			total.ChunksUploaded += stats.ChunksUploaded
			total.Bytes += stats.Bytes
			total.BytesUploaded += stats.BytesUploaded
			if opts.OnBlob != nil {
				opts.OnBlob(entry.Name(), info.Size(), skipped)
			}
			onBlobMu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if opts.OnTransfer != nil {
		opts.OnTransfer(total)
	}

	// Publish the tag last: every blob it references is already in the bucket, so
	// this single write is what makes the image visible, all at once.
	manifestPrefix := parsed.ManifestsPrefix()
	if err := client.PutObject(ctx, parsed.Bucket, manifestPrefix+tagManifestFile, manifestData); err != nil {
		return fmt.Errorf("publish tag: %w", err)
	}

	// Record push history (best-effort — don't fail the push on history errors).
	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				totalSize += info.Size()
			}
		}
	}
	if err := recordHistory(ctx, client, parsed, manifestData, totalSize); err != nil {
		slog.Debug("record history failed (non-fatal)", "error", err)
	}

	return nil
}
