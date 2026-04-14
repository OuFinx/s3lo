package image

import (
	"context"
	"fmt"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

func enforceTagWritePolicy(ctx context.Context, client storage.Backend, parsed ref.Reference, force bool) error {
	if force {
		return nil
	}

	cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
	if err != nil {
		return fmt.Errorf("check bucket config: %w", err)
	}
	if !cfg.IsImmutable(parsed.Image) {
		return nil
	}

	exists, err := client.HeadObjectExists(ctx, parsed.Bucket, parsed.ManifestsPrefix()+"manifest.json")
	if err != nil {
		return fmt.Errorf("check existing tag: %w", err)
	}
	if exists {
		return fmt.Errorf("tag %s already exists for %s (immutable). Use --force to overwrite", parsed.Tag, parsed.Image)
	}
	return nil
}
