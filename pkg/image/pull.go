package image

import (
	"context"
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// Pull downloads an OCI image from S3 and imports it into the local Docker daemon.
func Pull(ctx context.Context, s3Ref, imageTag string) error {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid S3 reference: %w", err)
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "s3lo-pull-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := client.DownloadDirectory(ctx, parsed.Bucket, parsed.S3Prefix(), tmpDir); err != nil {
		return fmt.Errorf("download from S3: %w", err)
	}

	// Use the image tag if provided, otherwise construct from ref
	if imageTag == "" {
		imageTag = parsed.Image + ":" + parsed.Tag
	}

	if err := oci.ImportImage(ctx, tmpDir, imageTag); err != nil {
		return fmt.Errorf("import into Docker: %w", err)
	}

	return nil
}
