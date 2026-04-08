package image

import (
	"context"
	"fmt"
	"os"

	"github.com/finx/s3lo/pkg/oci"
	"github.com/finx/s3lo/pkg/ref"
	s3client "github.com/finx/s3lo/pkg/s3"
)

// Push exports a local Docker image and uploads it to S3.
func Push(ctx context.Context, imageRef, s3Ref string) error {
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

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	if err := client.UploadDirectory(ctx, tmpDir, parsed.Bucket, parsed.S3Prefix()); err != nil {
		return fmt.Errorf("upload to S3: %w", err)
	}

	return nil
}
