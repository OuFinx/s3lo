package image

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// copyRegistryToS3 pulls an image from an OCI registry and pushes it to S3.
// Supports ECR (auto-auth via AWS SDK) and any OCI Distribution compatible registry.
// If the source is a multi-arch Image Index, all platforms are copied by default.
// All blobs are fetched and uploaded in parallel (up to 10 concurrent workers).
// Blobs are streamed directly from the registry to temp files without buffering in memory (#40).
func copyRegistryToS3(ctx context.Context, srcRef, destRef string, opts CopyOptions) (*CopyResult, error) {
	destParsed, err := ref.Parse(destRef)
	if err != nil {
		return nil, fmt.Errorf("invalid destination S3 reference: %w", err)
	}

	reg, image, tag, err := parseOCIRef(srcRef)
	if err != nil {
		return nil, fmt.Errorf("parse source reference: %w", err)
	}

	rc := newRegistryClient()
	// Seed token for ECR; non-ECR registries use 401 challenge flow.
	if initialToken, err := resolveAuth(ctx, reg); err != nil {
		return nil, fmt.Errorf("resolve registry auth: %w", err)
	} else if initialToken != "" {
		rc.setToken(reg, initialToken)
	}

	// Fetch top-level manifest (single-arch or multi-arch index).
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, image, tag)
	manifestData, _, err := rc.fetchManifest(ctx, manifestURL, reg, image)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest from registry: %w", err)
	}

	s3c, err := storage.NewBackendFromRef(ctx, destRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}
	if err := enforceTagWritePolicy(ctx, s3c, destParsed, opts.Force); err != nil {
		return nil, err
	}

	var blobsCopied, blobsSkipped atomic.Int64

	// fetchAndUploadBlob streams a blob from the registry to S3 via a temp file (#40).
	// platform="" suppresses OnBlob notification (used for internal blobs).
	fetchAndUploadBlob := func(ctx context.Context, digest string, knownSize int64, platform string) error {
		encoded := trimSHA256Prefix(digest)
		destKey := "blobs/sha256/" + encoded
		exists, _ := s3c.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if exists {
			blobsSkipped.Add(1)
			if platform != "" && opts.OnBlob != nil {
				opts.OnBlob(platform, encoded, knownSize, true)
			}
			slog.Debug("registry blob already exists, skipping", "digest", encoded[:12])
			return nil
		}

		blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", reg, image, digest)
		tmpPath, err := rc.fetchBlobToFile(ctx, blobURL, reg, image)
		if err != nil {
			return fmt.Errorf("fetch blob %s: %w", encoded[:12], err)
		}
		defer os.Remove(tmpPath)

		info, err := os.Stat(tmpPath)
		if err != nil {
			return fmt.Errorf("stat blob temp file: %w", err)
		}
		actualSize := info.Size()

		if err := s3c.UploadFile(ctx, tmpPath, destParsed.Bucket, destKey, storage.StorageClassIntelligentTiering); err != nil {
			return fmt.Errorf("upload blob %s: %w", encoded[:12], err)
		}
		blobsCopied.Add(1)
		if platform != "" && opts.OnBlob != nil {
			opts.OnBlob(platform, encoded, actualSize, false)
		}
		slog.Debug("registry blob uploaded", "digest", encoded[:12], "size", actualSize)
		return nil
	}

	// runParallel copies a deduplicated set of blob tasks with up to 10 workers.
	runParallel := func(tasks []blobTask) error {
		if opts.OnStart != nil {
			var totalBytes int64
			for _, t := range tasks {
				if t.platform != "" {
					totalBytes += t.size
				}
			}
			if totalBytes > 0 {
				opts.OnStart(totalBytes)
			}
		}
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(10)
		for _, t := range tasks {
			t := t
			g.Go(func() error {
				return fetchAndUploadBlob(gCtx, t.digest, t.size, t.platform)
			})
		}
		return g.Wait()
	}

	manifestPrefix := destParsed.ManifestsPrefix()
	ociLayout := []byte(`{"imageLayoutVersion":"1.0.0"}`)

	if isImageIndex(manifestData) {
		idx, err := parseIndex(manifestData)
		if err != nil {
			return nil, fmt.Errorf("parse image index: %w", err)
		}

		var selected []ocispec.Descriptor
		for _, desc := range idx.Manifests {
			if opts.Platform != "" && !matchesPlatform(desc, opts.Platform) {
				continue
			}
			selected = append(selected, desc)
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("no matching platform found (available: %s)", indexPlatformList(idx))
		}

		// Fetch all platform manifests in parallel.
		type platInfo struct {
			platform string
			data     []byte
			digest   string
		}
		platInfos := make([]platInfo, len(selected))
		{
			g, _ := errgroup.WithContext(ctx)
			for i, desc := range selected {
				i, desc := i, desc
				g.Go(func() error {
					platURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, image, desc.Digest.String())
					data, _, err := rc.fetchManifest(ctx, platURL, reg, image)
					if err != nil {
						return fmt.Errorf("fetch platform manifest %s: %w", platformString(desc.Platform), err)
					}
					platInfos[i] = platInfo{platformString(desc.Platform), data, desc.Digest.String()}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Store platform manifest blobs directly (already in memory — small JSON).
		{
			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(10)
			for _, pi := range platInfos {
				pi := pi
				g.Go(func() error {
					encoded := trimSHA256Prefix(pi.digest)
					destKey := "blobs/sha256/" + encoded
					exists, _ := s3c.HeadObjectExists(gCtx, destParsed.Bucket, destKey)
					if exists {
						return nil
					}
					return s3c.PutObject(gCtx, destParsed.Bucket, destKey, pi.data)
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Collect all unique content blobs (configs + layers) across platforms.
		seen := make(map[string]struct{})
		var tasks []blobTask
		for _, pi := range platInfos {
			var m struct {
				Config struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				} `json:"config"`
				Layers []struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(pi.data, &m); err != nil {
				return nil, fmt.Errorf("parse manifest for %s: %w", pi.platform, err)
			}
			if d := m.Config.Digest; d != "" {
				if _, ok := seen[d]; !ok {
					seen[d] = struct{}{}
					tasks = append(tasks, blobTask{d, m.Config.Size, pi.platform})
				}
			}
			for _, layer := range m.Layers {
				if d := layer.Digest; d != "" {
					if _, ok := seen[d]; !ok {
						seen[d] = struct{}{}
						tasks = append(tasks, blobTask{d, layer.Size, pi.platform})
					}
				}
			}
		}

		if err := runParallel(tasks); err != nil {
			return nil, err
		}

		// Write destination manifest.
		var writeManifestData []byte
		if opts.Platform != "" && len(selected) == 1 {
			writeManifestData = platInfos[0].data
		} else if opts.Platform != "" {
			filteredIdx := idx
			filteredIdx.Manifests = selected
			if writeManifestData, err = json.Marshal(filteredIdx); err != nil {
				return nil, fmt.Errorf("marshal filtered index: %w", err)
			}
		} else {
			writeManifestData = manifestData
		}

		if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"manifest.json", writeManifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
		if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"oci-layout", ociLayout); err != nil {
			return nil, fmt.Errorf("write oci-layout: %w", err)
		}

		_ = recordHistory(ctx, s3c, destParsed, writeManifestData, totalManifestSize(writeManifestData))

		return &CopyResult{
			Platforms:    len(selected),
			BlobsCopied:  int(blobsCopied.Load()),
			BlobsSkipped: int(blobsSkipped.Load()),
		}, nil
	}

	// Single-arch manifest: collect blobs, upload in parallel.
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	var tasks []blobTask
	if d := manifest.Config.Digest; d != "" {
		tasks = append(tasks, blobTask{d, manifest.Config.Size, "single"})
	}
	for _, layer := range manifest.Layers {
		if d := layer.Digest; d != "" {
			tasks = append(tasks, blobTask{d, layer.Size, "single"})
		}
	}
	if err := runParallel(tasks); err != nil {
		return nil, err
	}
	if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"manifest.json", manifestData); err != nil {
		return nil, fmt.Errorf("write manifest.json: %w", err)
	}
	if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"oci-layout", ociLayout); err != nil {
		return nil, fmt.Errorf("write oci-layout: %w", err)
	}

	_ = recordHistory(ctx, s3c, destParsed, manifestData, totalManifestSize(manifestData))

	return &CopyResult{
		Platforms:    1,
		BlobsCopied:  int(blobsCopied.Load()),
		BlobsSkipped: int(blobsSkipped.Load()),
	}, nil
}
