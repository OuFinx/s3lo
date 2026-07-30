package image

import (
	"context"
	"fmt"

	"github.com/OuFinx/s3lo/pkg/chunk"
	"github.com/OuFinx/s3lo/pkg/chunkstore"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// blobKey is where whole-layer blobs live, shared across every image in the bucket.
func blobKey(digest string) string { return "blobs/sha256/" + digest }

// fetchLayer materialises a layer at destPath, whichever way the bucket happens
// to store it, and verifies the result against digest either way.
//
// The recipe lookup costs one HEAD per layer on buckets that have never been
// chunked. That is the price of letting a bucket hold both forms at once, which
// is what makes enabling or disabling chunking a no-op rather than a migration.
func fetchLayer(ctx context.Context, client storage.Backend, bucket, digest, destPath string) error {
	recipe, chunked, err := chunkstore.LoadRecipe(ctx, client, bucket, digest)
	if err != nil {
		return err
	}
	if chunked {
		// Fetch verifies the assembled layer against recipe.Layer itself.
		return chunkstore.Fetch(ctx, client, bucket, recipe, destPath)
	}

	if err := client.DownloadObjectToFile(ctx, bucket, blobKey(digest), destPath); err != nil {
		return err
	}
	return verifyFileDigest(destPath, digest)
}

// storeLayer writes a layer to the bucket, chunked or whole, and reports whether
// anything actually had to be uploaded.
//
// Layers below one chunk are always stored whole: splitting them would add a
// recipe object and an indirection for no deduplication, since a sub-chunk layer
// produces exactly one chunk anyway.
func storeLayer(ctx context.Context, client storage.Backend, bucket, localPath, digest string,
	size int64, chunked bool) (chunkstore.Stats, bool, error) {

	if !chunked || size < chunk.MinSize {
		exists, err := client.HeadObjectExists(ctx, bucket, blobKey(digest))
		if err != nil {
			return chunkstore.Stats{}, false, fmt.Errorf("check blob %s: %w", short(digest), err)
		}
		if exists {
			// Refresh the timestamp so a concurrent GC keeps the blob inside its
			// grace window while this push writes the manifest that references it.
			if err := client.TouchObject(ctx, bucket, blobKey(digest)); err != nil {
				return chunkstore.Stats{}, true, nil
			}
			return chunkstore.Stats{}, true, nil
		}
		if err := client.UploadFile(ctx, localPath, bucket, blobKey(digest), storage.StorageClassIntelligentTiering); err != nil {
			return chunkstore.Stats{}, false, fmt.Errorf("upload blob %s: %w", short(digest), err)
		}
		return chunkstore.Stats{Bytes: size, BytesUploaded: size}, false, nil
	}

	stats, err := chunkstore.Store(ctx, client, bucket, localPath, digest)
	if err != nil {
		return stats, false, err
	}
	return stats, stats.BytesUploaded == 0, nil
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
