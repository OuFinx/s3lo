package image

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"encoding/json"
	"github.com/OuFinx/s3lo/v3/pkg/oci"
	"github.com/OuFinx/s3lo/v3/pkg/ref"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"sync"
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

// PullResult reports what a pull imported. Digest and Bytes are empty/zero for
// an image found only in the legacy v1.0.0 layout, which has no single manifest
// object to describe.
type PullResult struct {
	Ref      string `json:"ref" yaml:"ref"`
	ImageTag string `json:"image_tag" yaml:"image_tag"`
	Digest   string `json:"digest,omitempty" yaml:"digest,omitempty"`
	Bytes    int64  `json:"bytes" yaml:"bytes"`
}

// Pull downloads an OCI image from S3 and imports it into the local Docker daemon.
// Supports both v1.1.0 (global blobs/sha256/ + manifests/) and v1.0.0 (per-tag) layouts.
func Pull(ctx context.Context, s3Ref, imageTag string, opts PullOptions) (*PullResult, error) {
	var res PullResult
	if err := pull(ctx, s3Ref, imageTag, opts, &res); err != nil {
		return nil, err
	}
	res.Ref = s3Ref
	return &res, nil
}

func pull(ctx context.Context, s3Ref, imageTag string, opts PullOptions, res *PullResult) error {
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
		bytes, err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob, opts.OnStart)
		if err != nil {
			return err
		}
		res.Digest = digest.FromBytes(manifestData).String()
		res.Bytes = bytes
	}

	if imageTag == "" {
		imageTag = parsed.Image + ":" + parsed.Tag
	}

	if err := oci.ImportImage(ctx, tmpDir, imageTag); err != nil {
		return fmt.Errorf("import into Docker: %w", err)
	}

	res.ImageTag = imageTag
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
			if err := verifyBytesDigest(data, d); err != nil {
				return nil, fmt.Errorf("verify platform manifest for %s: %w", target, err)
			}
			return data, nil
		}
	}

	return nil, fmt.Errorf("platform %q not found in image index (available: %s)", target, indexPlatformList(idx))
}

// pullV110 downloads a v1.1.0 image into tmpDir, reconstructing the local OCI layout
// that oci.ImportImage expects: tmpDir/manifest.json + tmpDir/blobs/sha256/<digest>.
// It returns the total blob bytes the manifest describes.
func pullV110(ctx context.Context, client storage.Backend, parsed ref.Reference, manifestData []byte, tmpDir string, onBlob func(string, int64), onStart func(int64)) (int64, error) {
	manifest, err := oci.ParseManifest(manifestData)
	if err != nil {
		return 0, fmt.Errorf("parse manifest: %w", err)
	}

	totalBytes := manifest.Config.Size
	for _, layer := range manifest.Layers {
		totalBytes += layer.Size
	}
	// Report total download size for deterministic progress bar.
	if onStart != nil && totalBytes > 0 {
		onStart(totalBytes)
	}

	blobsDir := filepath.Join(tmpDir, "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		return 0, fmt.Errorf("create blobs dir: %w", err)
	}

	// Download config blob.
	configDigest := manifest.Config.Digest.Encoded()
	configPath := filepath.Join(blobsDir, configDigest)
	if err := client.DownloadObjectToFile(ctx, parsed.Bucket, "blobs/sha256/"+configDigest, configPath); err != nil {
		return 0, fmt.Errorf("download config blob: %w", err)
	}
	// Verify content against its digest — the store is content-addressable, so a
	// corrupted or tampered blob must never be handed to the Docker daemon.
	if err := verifyFileDigest(configPath, configDigest); err != nil {
		return 0, fmt.Errorf("verify config blob: %w", err)
	}
	if onBlob != nil {
		onBlob(configDigest, manifest.Config.Size)
	}

	// Download layer blobs in parallel.
	var mu sync.Mutex
	raw := make([]ocispec.Descriptor, len(manifest.Layers))
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(blobConcurrency)
	for i, layer := range manifest.Layers {
		i, layer := i, layer
		g.Go(func() error {
			d := layer.Digest.Encoded()
			// Resolves the layer whether the bucket stores it whole or as chunks,
			// and verifies it against its digest either way.
			rawDigest, rawSize, err := fetchLayer(gCtx, client, parsed.Bucket, d, blobsDir)
			if err != nil {
				return fmt.Errorf("fetch layer %s: %w", short(d), err)
			}
			mu.Lock()
			raw[i] = ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    digest.Digest("sha256:" + rawDigest),
				Size:      rawSize,
			}
			mu.Unlock()
			if onBlob != nil {
				onBlob(d, layer.Size)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, err
	}

	// The local layout describes the raw layers that were just written, which is
	// what the Docker daemon is given. A chunked bucket advertises the compressed
	// form; on disk here it is always the plain tar.
	local := manifest
	local.Layers = raw
	localData, err := json.Marshal(local)
	if err != nil {
		return 0, fmt.Errorf("marshal local manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), localData, 0o644); err != nil {
		return 0, fmt.Errorf("write manifest: %w", err)
	}
	return totalBytes, nil
}
