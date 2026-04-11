package image

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"golang.org/x/sync/errgroup"
)

// PullOptions controls pull behavior.
type PullOptions struct {
	// OnBlob is called for each blob after it is downloaded.
	// digest is the sha256 hex digest, size in bytes.
	OnBlob func(digest string, size int64)
}

// Pull downloads an OCI image from S3 and imports it into the local Docker daemon.
// Supports both v1.1.0 (global blobs/sha256/ + manifests/) and v1.0.0 (per-tag) layouts.
func Pull(ctx context.Context, s3Ref, imageTag string, opts PullOptions) error {
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

	// Try v1.1.0 layout first.
	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if !errors.As(err, &noSuchKey) {
			return fmt.Errorf("fetch manifest: %w", err)
		}
		// v1.1.0 not found — fall back to v1.0.0 per-tag layout.
		if err := client.DownloadDirectory(ctx, parsed.Bucket, parsed.S3Prefix(), tmpDir); err != nil {
			return fmt.Errorf("download from S3: %w", err)
		}
	} else {
		if err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob); err != nil {
			return err
		}
	}

	if imageTag == "" {
		imageTag = parsed.Image + ":" + parsed.Tag
	}

	if err := oci.ImportImage(ctx, tmpDir, imageTag); err != nil {
		return fmt.Errorf("import into Docker: %w", err)
	}

	return nil
}

// pullV110 downloads a v1.1.0 image into tmpDir, reconstructing the local OCI layout
// that oci.ImportImage expects: tmpDir/manifest.json + tmpDir/blobs/sha256/<digest>.
func pullV110(ctx context.Context, client *s3client.Client, parsed ref.Reference, manifestData []byte, tmpDir string, onBlob func(string, int64)) error {
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestData, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	manifest, err := oci.ParseManifest(manifestData)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	blobsDir := filepath.Join(tmpDir, "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		return fmt.Errorf("create blobs dir: %w", err)
	}

	// Download config blob.
	configDigest := manifest.Config.Digest.Encoded()
	if err := client.DownloadObjectToFile(ctx, parsed.Bucket, "blobs/sha256/"+configDigest, filepath.Join(blobsDir, configDigest)); err != nil {
		return fmt.Errorf("download config blob: %w", err)
	}
	if onBlob != nil {
		onBlob(configDigest, manifest.Config.Size)
	}

	// Download layer blobs in parallel.
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	for _, layer := range manifest.Layers {
		layer := layer
		g.Go(func() error {
			d := layer.Digest.Encoded()
			if err := client.DownloadObjectToFile(gCtx, parsed.Bucket, "blobs/sha256/"+d, filepath.Join(blobsDir, d)); err != nil {
				return err
			}
			if onBlob != nil {
				onBlob(d, layer.Size)
			}
			return nil
		})
	}
	return g.Wait()
}
