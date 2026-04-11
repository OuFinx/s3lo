package image

import (
	"context"
	"fmt"
	"strings"

	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// MigrateResult summarizes the outcome of a migration run.
type MigrateResult struct {
	Images     int
	BlobsMoved int
}

// Migrate converts images from the v1.0.0 per-tag layout to the v1.1.0 global blob layout.
// It is idempotent: safe to run multiple times.
//
// For each v1.0.0 image tag found at <image>/<tag>/:
//  - blobs are copied to blobs/sha256/<digest> (skipped if already present)
//  - manifest files are moved to manifests/<image>/<tag>/
//  - old per-tag objects are deleted
func Migrate(ctx context.Context, s3BucketRef string) (*MigrateResult, error) {
	bucket, prefix, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}
	s3c, err := client.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("get S3 client for bucket: %w", err)
	}

	// Find top-level image prefixes, skipping v1.1.0 reserved names.
	imagePrefixes, err := listCommonPrefixes(ctx, s3c, bucket, prefix, "/")
	if err != nil {
		return nil, fmt.Errorf("list image prefixes: %w", err)
	}

	result := &MigrateResult{}
	for _, imagePfx := range imagePrefixes {
		imageName := strings.TrimPrefix(imagePfx, prefix)
		imageName = strings.TrimSuffix(imageName, "/")

		if imageName == "blobs" || imageName == "manifests" {
			continue
		}

		tagPrefixes, err := listCommonPrefixes(ctx, s3c, bucket, imagePfx, "/")
		if err != nil {
			return nil, fmt.Errorf("list tags for %s: %w", imageName, err)
		}

		for _, tagPfx := range tagPrefixes {
			tagName := strings.TrimPrefix(tagPfx, imagePfx)
			tagName = strings.TrimSuffix(tagName, "/")

			fmt.Printf("  migrating %s:%s\n", imageName, tagName)
			blobs, err := migrateTag(ctx, client, bucket, prefix, imageName, tagName)
			if err != nil {
				return nil, fmt.Errorf("migrate %s:%s: %w", imageName, tagName, err)
			}
			result.Images++
			result.BlobsMoved += blobs
		}
	}
	return result, nil
}

// migrateTag migrates a single image tag from v1.0.0 to v1.1.0 layout.
// Returns the number of blobs copied.
func migrateTag(ctx context.Context, client *s3client.Client, bucket, prefix, image, tag string) (int, error) {
	oldPrefix := prefix + image + "/" + tag + "/"
	newManifestPrefix := prefix + "manifests/" + image + "/" + tag + "/"
	newBlobsPrefix := prefix + "blobs/sha256/"

	keys, err := client.ListKeys(ctx, bucket, oldPrefix)
	if err != nil {
		return 0, fmt.Errorf("list old objects: %w", err)
	}

	blobsCopied := 0
	var oldKeys []string

	for _, key := range keys {
		rel := strings.TrimPrefix(key, oldPrefix)

		var destKey string
		if strings.HasPrefix(rel, "blobs/sha256/") {
			digest := strings.TrimPrefix(rel, "blobs/sha256/")
			destKey = newBlobsPrefix + digest
			blobsCopied++
		} else {
			destKey = newManifestPrefix + rel
		}

		if err := client.CopyObject(ctx, bucket, key, destKey); err != nil {
			return 0, fmt.Errorf("copy %s → %s: %w", key, destKey, err)
		}
		oldKeys = append(oldKeys, key)
	}

	if err := client.DeleteObjects(ctx, bucket, oldKeys); err != nil {
		return 0, fmt.Errorf("delete old objects: %w", err)
	}

	return blobsCopied, nil
}
