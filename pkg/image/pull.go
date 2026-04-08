package image

import (
	"context"
	"fmt"

	"github.com/finx/s3lo/pkg/ref"
	s3client "github.com/finx/s3lo/pkg/s3"
)

// Pull downloads an OCI image from S3 to a local directory.
func Pull(s3Ref, destDir string) error {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid S3 reference: %w", err)
	}

	fmt.Printf("Pulling s3://%s/%s...\n", parsed.Bucket, parsed.S3Prefix())

	client, err := s3client.NewClient()
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}

	if err := client.DownloadDirectory(context.Background(), parsed.Bucket, parsed.S3Prefix(), destDir); err != nil {
		return fmt.Errorf("download from S3: %w", err)
	}

	fmt.Printf("Done. Image saved to %s\n", destDir)
	return nil
}
