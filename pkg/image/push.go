package image

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// manifestFiles are the per-tag metadata files uploaded to manifests/<image>/<tag>/.
var manifestFiles = []string{"manifest.json", "config.json", "index.json", "oci-layout"}

// Push exports a local Docker image and uploads it to S3 using the v1.1.0 layout:
//   - blobs → blobs/sha256/<digest>  (global, Intelligent-Tiering, cross-image dedup)
//   - manifests → manifests/<image>/<tag>/  (Standard storage class)
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

	// Upload blobs to global blobs/sha256/ with Intelligent-Tiering.
	blobsDir := filepath.Join(tmpDir, "blobs", "sha256")
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		return fmt.Errorf("read blobs dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		localPath := filepath.Join(blobsDir, entry.Name())
		key := "blobs/sha256/" + entry.Name()
		if err := client.UploadFile(ctx, localPath, parsed.Bucket, key, s3types.StorageClassIntelligentTiering); err != nil {
			return fmt.Errorf("upload blob %s: %w", entry.Name(), err)
		}
	}

	// Upload manifest files to manifests/<image>/<tag>/ with Standard storage class.
	manifestPrefix := parsed.ManifestsPrefix()
	for _, name := range manifestFiles {
		localPath := filepath.Join(tmpDir, name)
		if _, statErr := os.Stat(localPath); os.IsNotExist(statErr) {
			continue
		}
		key := manifestPrefix + name
		if err := client.UploadFile(ctx, localPath, parsed.Bucket, key, ""); err != nil {
			return fmt.Errorf("upload %s: %w", name, err)
		}
	}

	return nil
}
