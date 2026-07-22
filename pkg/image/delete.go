package image

import (
	"context"
	"fmt"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// Delete removes an image tag from S3 by deleting all files under manifests/<image>/<tag>/.
// Only works with v1.1.0 layout. Blobs in blobs/sha256/ are NOT deleted — use GC for that.
// If the image is configured immutable, deletion is refused unless force is true, so the
// immutability guarantee also covers removal, not just overwrite.
func Delete(ctx context.Context, s3Ref string, force bool) error {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid S3 reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	if !force {
		cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
		if err != nil {
			return fmt.Errorf("check bucket config: %w", err)
		}
		if cfg.IsImmutable(parsed.Image) {
			return fmt.Errorf("tag %s for %s is immutable and cannot be deleted. Use --force to override", parsed.Tag, parsed.Image)
		}
	}

	prefix := parsed.ManifestsPrefix()
	keys, err := client.ListKeys(ctx, parsed.Bucket, prefix)
	if err != nil {
		return fmt.Errorf("list manifest files: %w", err)
	}

	if len(keys) == 0 {
		return fmt.Errorf("image %s not found", s3Ref)
	}

	if err := client.DeleteObjects(ctx, parsed.Bucket, keys); err != nil {
		return fmt.Errorf("delete manifest files: %w", err)
	}

	return nil
}
