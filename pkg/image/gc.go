package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	storage "github.com/OuFinx/s3lo/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// GCResult summarizes the outcome of a GC run.
type GCResult struct {
	Scanned    int
	Deleted    int
	FreedBytes int64
	DryRun     bool
}

// gcGracePeriod protects recently uploaded blobs from deletion to avoid
// racing with concurrent pushes.
const gcGracePeriod = time.Hour

// GC removes blobs in blobs/sha256/ that are not referenced by any manifest.
// If dryRun is true, no deletions are performed (safe to run at any time).
func GC(ctx context.Context, s3BucketRef string, dryRun bool) (*GCResult, error) {
	bucket, prefix, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	// Step 1: collect all blob digests referenced by any manifest.
	referenced, err := collectReferencedDigests(ctx, client, bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("collect referenced digests: %w", err)
	}

	// Step 2: list all blobs with metadata.
	blobsPrefix := prefix + "blobs/sha256/"
	blobs, err := client.ListObjectsWithMeta(ctx, bucket, blobsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}

	now := time.Now()
	result := &GCResult{Scanned: len(blobs), DryRun: dryRun}

	var toDelete []string
	for _, blob := range blobs {
		digest := strings.TrimPrefix(blob.Key, blobsPrefix)
		if referenced[digest] {
			continue
		}
		// Grace period: never delete blobs uploaded within the last hour.
		if now.Sub(blob.LastModified) < gcGracePeriod {
			continue
		}
		toDelete = append(toDelete, blob.Key)
		result.FreedBytes += blob.Size
	}

	result.Deleted = len(toDelete)

	if !dryRun && len(toDelete) > 0 {
		if err := client.DeleteObjects(ctx, bucket, toDelete); err != nil {
			return nil, fmt.Errorf("delete unreferenced blobs: %w", err)
		}
	}

	return result, nil
}

// collectReferencedDigests fetches all manifests in parallel and returns the set
// of blob digests (without sha256: prefix) they reference.
func collectReferencedDigests(ctx context.Context, client storage.Backend, bucket, prefix string) (map[string]bool, error) {
	manifestsPrefix := prefix + "manifests/"
	manifestKeys, err := client.ListKeys(ctx, bucket, manifestsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	// Filter to manifest.json keys only.
	var keys []string
	for _, key := range manifestKeys {
		if strings.HasSuffix(key, "/manifest.json") {
			keys = append(keys, key)
		}
	}

	var (
		mu         sync.Mutex
		referenced = make(map[string]bool)
	)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(20)

	for _, key := range keys {
		key := key
		g.Go(func() error {
			data, err := client.GetObject(ctx, bucket, key)
			if err != nil {
				return fmt.Errorf("fetch manifest %s: %w", key, err)
			}

			var manifest struct {
				Config struct {
					Digest string `json:"digest"`
				} `json:"config"`
				Layers []struct {
					Digest string `json:"digest"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return fmt.Errorf("parse manifest %s: %w", key, err)
			}

			mu.Lock()
			if d := trimSHA256Prefix(manifest.Config.Digest); d != "" {
				referenced[d] = true
			}
			for _, layer := range manifest.Layers {
				if d := trimSHA256Prefix(layer.Digest); d != "" {
					referenced[d] = true
				}
			}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return referenced, nil
}

func trimSHA256Prefix(digest string) string {
	return strings.TrimPrefix(digest, "sha256:")
}

