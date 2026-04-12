package image

import (
	"context"
	"fmt"

	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// Delete removes an image tag from S3 by deleting all files under manifests/<image>/<tag>/.
// Only works with v1.1.0 layout. Blobs in blobs/sha256/ are NOT deleted — use GC for that.
func Delete(ctx context.Context, s3Ref string) error {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid S3 reference: %w", err)
	}

	client, err := s3client.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
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
