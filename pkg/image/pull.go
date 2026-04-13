package image

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// PullOptions controls pull behavior.
type PullOptions struct {
	// Platform selects a specific platform from a multi-arch image (e.g. "linux/amd64").
	// Empty means auto-detect the host platform.
	Platform string
	// OnStart is called once with the total blob bytes before any downloads begin.
	OnStart func(totalBytes int64)
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

	if strings.HasPrefix(s3Ref, "local://") {
		if _, statErr := os.Stat(parsed.Bucket); os.IsNotExist(statErr) {
			return fmt.Errorf("local storage directory not found: %s", parsed.Bucket)
		}
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "s3lo-pull-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	slog.Debug("pulling image", "bucket", parsed.Bucket, "image", parsed.Image, "tag", parsed.Tag)

	// Try v1.1.0 layout first.
	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		if !storage.IsNotFound(err) {
			return fmt.Errorf("fetch manifest: %w", err)
		}
		// v1.1.0 not found — fall back to v1.0.0 per-tag layout.
		if err := client.DownloadDirectory(ctx, parsed.Bucket, parsed.S3Prefix(), tmpDir); err != nil {
			return fmt.Errorf("download from S3: %w", err)
		}
	} else {
		// Check if this is a multi-arch image index.
		if isImageIndex(manifestData) {
			manifestData, err = resolvePlatformManifest(ctx, client, parsed.Bucket, manifestData, opts.Platform)
			if err != nil {
				return err
			}
		}
		if err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob, opts.OnStart); err != nil {
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

// resolvePlatformManifest selects a platform from an Image Index and returns its manifest bytes.
// If platform is empty, the host platform is used.
func resolvePlatformManifest(ctx context.Context, client storage.Backend, bucket string, indexData []byte, platform string) ([]byte, error) {
	idx, err := parseIndex(indexData)
	if err != nil {
		return nil, fmt.Errorf("parse image index: %w", err)
	}

	target := platform
	if target == "" {
		target = hostPlatform()
	}

	for _, desc := range idx.Manifests {
		if matchesPlatform(desc, target) {
			d := desc.Digest.Encoded()
			data, err := client.GetObject(ctx, bucket, "blobs/sha256/"+d)
			if err != nil {
				return nil, fmt.Errorf("fetch platform manifest for %s: %w", target, err)
			}
			return data, nil
		}
	}

	return nil, fmt.Errorf("platform %q not found in image index (available: %s)", target, indexPlatformList(idx))
}

// pullV110 downloads a v1.1.0 image into tmpDir, reconstructing the local OCI layout
// that oci.ImportImage expects: tmpDir/manifest.json + tmpDir/blobs/sha256/<digest>.
func pullV110(ctx context.Context, client storage.Backend, parsed ref.Reference, manifestData []byte, tmpDir string, onBlob func(string, int64), onStart func(int64)) error {
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestData, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	manifest, err := oci.ParseManifest(manifestData)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	// Report total download size for deterministic progress bar.
	if onStart != nil {
		totalBytes := manifest.Config.Size
		for _, layer := range manifest.Layers {
			totalBytes += layer.Size
		}
		if totalBytes > 0 {
			onStart(totalBytes)
		}
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
