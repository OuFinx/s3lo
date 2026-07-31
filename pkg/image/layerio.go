package image

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/OuFinx/s3lo/pkg/chunk"
	"github.com/OuFinx/s3lo/pkg/chunkstore"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// blobKey is where whole-layer blobs live, shared across every image in the bucket.
func blobKey(digest string) string { return "blobs/sha256/" + digest }

// fetchLayer materialises a layer as a raw tar in dir, whichever way the bucket
// stores it, and returns the raw layer's digest and size.
//
// digest is what the manifest advertises: for a chunked layer that is the
// compressed stream's digest, and the raw digest that comes back differs. Callers
// handing the result to a container runtime need the raw identity, so it is
// returned rather than assumed.
//
// The recipe lookup costs one HEAD per layer on buckets that have never been
// chunked. That is the price of letting a bucket hold both forms at once, which
// is what makes enabling or disabling chunking a no-op rather than a migration.
func fetchLayer(ctx context.Context, client storage.Backend, bucket, digest, dir string) (string, int64, error) {
	recipe, chunked, err := chunkstore.LoadRecipe(ctx, client, bucket, digest)
	if err != nil {
		return "", 0, err
	}
	if chunked {
		// Fetch verifies the assembled layer against recipe.Layer itself.
		dest := filepath.Join(dir, recipe.Layer)
		if err := chunkstore.Fetch(ctx, client, bucket, recipe, dest); err != nil {
			return "", 0, err
		}
		return recipe.Layer, recipe.Size, nil
	}

	dest := filepath.Join(dir, digest)
	if err := client.DownloadObjectToFile(ctx, bucket, blobKey(digest), dest); err != nil {
		return "", 0, err
	}
	if err := verifyFileDigest(dest, digest); err != nil {
		return "", 0, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return "", 0, err
	}
	return digest, info.Size(), nil
}

// storeLayer writes a layer to the bucket, chunked or whole, and reports whether
// anything actually had to be uploaded.
//
// Layers below one chunk are always stored whole: splitting them would add a
// recipe object and an indirection for no deduplication, since a sub-chunk layer
// produces exactly one chunk anyway.
func storeLayer(ctx context.Context, client storage.Backend, bucket, localPath, digest string,
	size int64, chunked bool) (chunkstore.Recipe, chunkstore.Stats, bool, error) {

	if !chunked || size < chunk.MinSize {
		exists, err := client.HeadObjectExists(ctx, bucket, blobKey(digest))
		if err != nil {
			return chunkstore.Recipe{}, chunkstore.Stats{}, false, fmt.Errorf("check blob %s: %w", short(digest), err)
		}
		if exists {
			// Refresh the timestamp so a concurrent GC keeps the blob inside its
			// grace window while this push writes the manifest that references it.
			if err := client.TouchObject(ctx, bucket, blobKey(digest)); err != nil {
				return chunkstore.Recipe{}, chunkstore.Stats{}, true, nil
			}
			return chunkstore.Recipe{}, chunkstore.Stats{}, true, nil
		}
		if err := client.UploadFile(ctx, localPath, bucket, blobKey(digest), storage.StorageClassIntelligentTiering); err != nil {
			return chunkstore.Recipe{}, chunkstore.Stats{}, false, fmt.Errorf("upload blob %s: %w", short(digest), err)
		}
		return chunkstore.Recipe{}, chunkstore.Stats{Bytes: size, BytesUploaded: size}, false, nil
	}

	recipe, stats, err := chunkstore.Store(ctx, client, bucket, localPath, digest)
	if err != nil {
		return recipe, stats, false, err
	}
	return recipe, stats, stats.BytesUploaded == 0, nil
}

// copyChunkedLayer transfers a layer that the source bucket stores as chunks,
// reproducing it in the same form at the destination. It returns false when the
// source has no recipe for this digest, leaving the caller to copy a whole blob.
//
// Copying chunk by chunk keeps deduplication intact across the copy: a
// destination that already shares chunks with the source only receives what it
// is actually missing.
func copyChunkedLayer(ctx context.Context, srcClient, destClient storage.Backend,
	srcBucket, destBucket, digest string) (bool, error) {

	recipe, chunked, err := chunkstore.LoadRecipe(ctx, srcClient, srcBucket, digest)
	if err != nil {
		return false, err
	}
	if !chunked {
		return false, nil
	}

	for _, c := range recipe.Chunks {
		key := chunkstore.ChunkKey(c.Digest)
		exists, err := destClient.HeadObjectExists(ctx, destBucket, key)
		if err != nil {
			return true, fmt.Errorf("check chunk %s: %w", short(c.Digest), err)
		}
		if exists {
			continue
		}
		data, err := srcClient.GetObject(ctx, srcBucket, key)
		if err != nil {
			return true, fmt.Errorf("fetch chunk %s: %w", short(c.Digest), err)
		}
		if err := destClient.PutObject(ctx, destBucket, key, data); err != nil {
			return true, fmt.Errorf("write chunk %s: %w", short(c.Digest), err)
		}
	}

	// The recipe is written last: until it exists the layer is not resolvable,
	// which matches how push publishes a tag only once its blobs are in place.
	recipeData, err := srcClient.GetObject(ctx, srcBucket, chunkstore.RecipeKey(digest))
	if err != nil {
		return true, fmt.Errorf("fetch recipe %s: %w", short(digest), err)
	}
	if err := destClient.PutObject(ctx, destBucket, chunkstore.RecipeKey(digest), recipeData); err != nil {
		return true, fmt.Errorf("write recipe %s: %w", short(digest), err)
	}
	return true, nil
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
