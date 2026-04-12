package image

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"golang.org/x/sync/errgroup"
)

// manifestFiles are the per-tag metadata files uploaded to manifests/<image>/<tag>/.
var manifestFiles = []string{"manifest.json", "config.json", "index.json", "oci-layout"}

// PushOptions controls push behavior.
type PushOptions struct {
	// Force overwrites an existing tag even if the bucket has immutability enabled.
	Force bool
	// OnStart is called once with the total blob bytes before any uploads begin.
	OnStart func(totalBytes int64)
	// OnBlob is called for each blob after it is processed (uploaded or skipped).
	// digest is the sha256 digest (without "sha256:" prefix), size in bytes, skipped=true if already existed.
	OnBlob func(digest string, size int64, skipped bool)
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

	client, err := s3client.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	// Immutability check: reject push if tag exists and image is configured immutable.
	if !opts.Force {
		cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
		if err != nil {
			return fmt.Errorf("check bucket config: %w", err)
		}
		if cfg.IsImmutable(parsed.Image) {
			exists, err := client.HeadObjectExists(ctx, parsed.Bucket, parsed.ManifestsPrefix()+"manifest.json")
			if err != nil {
				return fmt.Errorf("check existing tag: %w", err)
			}
			if exists {
				return fmt.Errorf("tag %s already exists for %s (immutable). Use --force to overwrite", parsed.Tag, parsed.Image)
			}
		}
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

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	var onBlobMu sync.Mutex

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entry := entry
		g.Go(func() error {
			localPath := filepath.Join(blobsDir, entry.Name())
			key := "blobs/sha256/" + entry.Name()

			info, err := os.Stat(localPath)
			if err != nil {
				return fmt.Errorf("stat blob %s: %w", entry.Name(), err)
			}

			// Single dedup check — UploadFile skips internally when the object exists,
			// but we need to know the outcome for OnBlob reporting.
			exists, _ := client.HeadObjectExists(gCtx, parsed.Bucket, key)
			if !exists {
				slog.Debug("uploading blob", "digest", entry.Name()[:12], "size", info.Size())
				if err := client.UploadFile(gCtx, localPath, parsed.Bucket, key, s3types.StorageClassIntelligentTiering); err != nil {
					return fmt.Errorf("upload blob %s: %w", entry.Name(), err)
				}
			} else {
				slog.Debug("blob already exists, skipping", "digest", entry.Name()[:12])
			}

			if opts.OnBlob != nil {
				onBlobMu.Lock()
				opts.OnBlob(entry.Name(), info.Size(), exists)
				onBlobMu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Upload manifest files to manifests/<image>/<tag>/ with Standard storage class.
	manifestPrefix := parsed.ManifestsPrefix()
	for _, name := range manifestFiles {
		localPath := filepath.Join(tmpDir, name)
		if _, statErr := os.Stat(localPath); os.IsNotExist(statErr) {
			continue
		}
		key := manifestPrefix + name
		if err := client.UploadFile(ctx, localPath, parsed.Bucket, key, ""); err != nil {
			return fmt.Errorf("upload %s: %w", name, err)
		}
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
