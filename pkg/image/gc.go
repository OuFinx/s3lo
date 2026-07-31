package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OuFinx/s3lo/pkg/chunkstore"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// GCResult summarizes the outcome of a GC run. Scanned, Deleted and FreedBytes
// are totals across every object class GC sweeps (blobs, recipes and chunks).
type GCResult struct {
	Scanned    int
	Deleted    int
	FreedBytes int64
	DryRun     bool

	// ChunksScanned and ChunksDeleted break out the chunk store, which is empty
	// on buckets that have never had a chunked push.
	ChunksScanned int
	ChunksDeleted int
}

// gcGracePeriod protects recently uploaded blobs from deletion to avoid
// racing with concurrent pushes.
const gcGracePeriod = time.Hour

// GC removes blobs in blobs/sha256/ that are not referenced by any manifest.
// If dryRun is true, no deletions are performed (safe to run at any time).
func GC(ctx context.Context, s3BucketRef string, dryRun bool) (*GCResult, error) {
	bucket, err := ParseBucketRootRef(s3BucketRef)
	if err != nil {
		return nil, err
	}
	prefix := ""

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	// Step 1: collect all blob digests referenced by any manifest.
	referenced, err := collectReferencedDigests(ctx, client, bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("collect referenced digests: %w", err)
	}

	// Step 2: list every object class GC owns.
	blobsPrefix := prefix + "blobs/sha256/"
	blobs, err := client.ListObjectsWithMeta(ctx, bucket, blobsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}

	recipesPrefix := prefix + chunkstore.RecipesPrefix
	recipes, err := client.ListObjectsWithMeta(ctx, bucket, recipesPrefix)
	if err != nil {
		return nil, fmt.Errorf("list recipes: %w", err)
	}

	chunksPrefix := prefix + chunkstore.ChunksPrefix
	chunks, err := client.ListObjectsWithMeta(ctx, bucket, chunksPrefix)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}

	// Step 3: a chunked layer is reachable as manifest -> recipe -> chunks.
	// Without this expansion every chunk looks unreferenced and GC would empty
	// the chunk store on its first run.
	referencedChunks, err := collectChunkReferences(ctx, client, bucket, recipesPrefix, recipes, referenced)
	if err != nil {
		return nil, fmt.Errorf("collect chunk references: %w", err)
	}

	now := time.Now()
	result := &GCResult{
		Scanned:       len(blobs) + len(recipes) + len(chunks),
		ChunksScanned: len(chunks),
		DryRun:        dryRun,
	}

	toDelete, freed := sweep(blobs, blobsPrefix, referenced, now)
	result.FreedBytes += freed

	// Recipes are keyed by the layer digest they rebuild, so they are reachable
	// under exactly the same reference set as whole-layer blobs.
	recipeDelete, recipeFreed := sweep(recipes, recipesPrefix, referenced, now)
	toDelete = append(toDelete, recipeDelete...)
	result.FreedBytes += recipeFreed

	chunkDelete, chunkFreed := sweep(chunks, chunksPrefix, referencedChunks, now)
	toDelete = append(toDelete, chunkDelete...)
	result.FreedBytes += chunkFreed
	result.ChunksDeleted = len(chunkDelete)

	result.Deleted = len(toDelete)

	if !dryRun && len(toDelete) > 0 {
		if err := client.DeleteObjects(ctx, bucket, toDelete); err != nil {
			return nil, fmt.Errorf("delete unreferenced objects: %w", err)
		}
	}

	return result, nil
}

// sweep selects objects under keyPrefix whose digest is not in referenced and
// which are older than the grace period. The grace period is what keeps a
// concurrent push safe: its objects are written before the manifest that makes
// them reachable, so for that window they are legitimately unreferenced.
func sweep(objs []storage.ObjectMeta, keyPrefix string, referenced map[string]bool, now time.Time) ([]string, int64) {
	var (
		toDelete []string
		freed    int64
	)
	for _, obj := range objs {
		digest := strings.TrimPrefix(obj.Key, keyPrefix)
		if referenced[digest] {
			continue
		}
		if now.Sub(obj.LastModified) < gcGracePeriod {
			continue
		}
		toDelete = append(toDelete, obj.Key)
		freed += obj.Size
	}
	return toDelete, freed
}

// collectChunkReferences reads the recipes that are still reachable and returns
// the set of chunk digests they depend on. Recipes that are themselves
// unreferenced are skipped, so their chunks are only kept alive by some other
// recipe that still needs them.
func collectChunkReferences(ctx context.Context, client storage.Backend, bucket, recipesPrefix string,
	recipes []storage.ObjectMeta, referenced map[string]bool) (map[string]bool, error) {

	var (
		mu               sync.Mutex
		referencedChunks = make(map[string]bool)
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(scanConcurrency)

	for _, obj := range recipes {
		layerDigest := strings.TrimPrefix(obj.Key, recipesPrefix)
		if !referenced[layerDigest] {
			continue
		}
		key := obj.Key
		g.Go(func() error {
			data, err := client.GetObject(gCtx, bucket, key)
			if err != nil {
				return fmt.Errorf("fetch recipe %s: %w", key, err)
			}
			var recipe chunkstore.Recipe
			if err := json.Unmarshal(data, &recipe); err != nil {
				return fmt.Errorf("parse recipe %s: %w", key, err)
			}
			mu.Lock()
			for _, c := range recipe.Chunks {
				referencedChunks[c.Digest] = true
			}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return referencedChunks, nil
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
	g.SetLimit(scanConcurrency)

	for _, key := range keys {
		key := key
		g.Go(func() error {
			data, err := client.GetObject(ctx, bucket, key)
			if err != nil {
				return fmt.Errorf("fetch manifest %s: %w", key, err)
			}

			refs, err := collectManifestReferences(ctx, client, bucket, data)
			if err != nil {
				return fmt.Errorf("parse manifest %s: %w", key, err)
			}

			mu.Lock()
			for digest := range refs {
				referenced[digest] = true
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
