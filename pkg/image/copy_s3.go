package image

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// copyBetweenBackends copies an image between storage backends (S3, GCS, Azure, local).
// Uses server-side CopyObject within the same bucket/scheme; streams cross-backend via temp files.
// All blobs are copied in parallel (up to 10 concurrent workers).
func copyBetweenBackends(ctx context.Context, srcRef, destRef string, opts CopyOptions) (*CopyResult, error) {
	srcParsed, err := ref.Parse(srcRef)
	if err != nil {
		return nil, fmt.Errorf("invalid source S3 reference: %w", err)
	}
	destParsed, err := ref.Parse(destRef)
	if err != nil {
		return nil, fmt.Errorf("invalid destination S3 reference: %w", err)
	}

	srcClient, err := storage.NewBackendFromRef(ctx, srcRef)
	if err != nil {
		return nil, fmt.Errorf("create source storage client: %w", err)
	}
	destClient, err := storage.NewBackendFromRef(ctx, destRef)
	if err != nil {
		return nil, fmt.Errorf("create destination storage client: %w", err)
	}
	if err := enforceTagWritePolicy(ctx, destClient, destParsed, opts.Force); err != nil {
		return nil, err
	}

	manifestKey := srcParsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := srcClient.GetObject(ctx, srcParsed.Bucket, manifestKey)
	if err != nil {
		return nil, fmt.Errorf("fetch source manifest: %w", err)
	}

	sameBucket := srcParsed.Bucket == destParsed.Bucket && srcParsed.Scheme == destParsed.Scheme
	var blobsCopied, blobsSkipped atomic.Int64

	copyBlob := func(ctx context.Context, digest string, size int64, platform string) error {
		srcKey := "blobs/sha256/" + digest
		destKey := "blobs/sha256/" + digest
		exists, err := destClient.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if err != nil {
			return fmt.Errorf("check destination blob %s: %w", digest[:12], err)
		}
		if exists {
			blobsSkipped.Add(1)
			if platform != "" && opts.OnBlob != nil {
				opts.OnBlob(platform, digest, size, true)
			}
			slog.Debug("blob already exists, skipping", "digest", digest[:12])
			return nil
		}
		if sameBucket {
			if err := destClient.CopyObject(ctx, destParsed.Bucket, srcKey, destKey); err != nil {
				return fmt.Errorf("copy blob %s: %w", digest[:12], err)
			}
		} else {
			tmp, err := os.CreateTemp("", "s3lo-copy-blob-*")
			if err != nil {
				return fmt.Errorf("create temp file for blob %s: %w", digest[:12], err)
			}
			tmpName := tmp.Name()
			tmp.Close()
			defer os.Remove(tmpName)
			if err := srcClient.DownloadObjectToFile(ctx, srcParsed.Bucket, srcKey, tmpName); err != nil {
				return fmt.Errorf("download blob %s: %w", digest[:12], err)
			}
			if err := destClient.UploadFile(ctx, tmpName, destParsed.Bucket, destKey, storage.StorageClassIntelligentTiering); err != nil {
				return fmt.Errorf("upload blob %s: %w", digest[:12], err)
			}
		}
		blobsCopied.Add(1)
		if platform != "" && opts.OnBlob != nil {
			opts.OnBlob(platform, digest, size, false)
		}
		slog.Debug("blob copied", "digest", digest[:12], "size", size)
		return nil
	}

	runParallel := func(tasks []blobTask) error {
		// Report total bytes for deterministic progress bar — only count user-facing blobs.
		if opts.OnStart != nil {
			var totalBytes int64
			for _, t := range tasks {
				if t.platform != "" {
					totalBytes += t.size
				}
			}
			opts.OnStart(totalBytes)
		}
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(blobConcurrency)
		for _, t := range tasks {
			t := t
			g.Go(func() error {
				return copyBlob(gCtx, t.digest, t.size, t.platform)
			})
		}
		return g.Wait()
	}

	destPrefix := destParsed.ManifestsPrefix()
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
			g, gCtx := errgroup.WithContext(ctx)
			for i, desc := range selected {
				i, desc := i, desc
				g.Go(func() error {
					d := desc.Digest.Encoded()
					data, err := srcClient.GetObject(gCtx, srcParsed.Bucket, "blobs/sha256/"+d)
					if err != nil {
						return fmt.Errorf("fetch manifest %s: %w", platformString(desc.Platform), err)
					}
					platInfos[i] = platInfo{platformString(desc.Platform), data, d}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Collect all unique blobs across platforms (deduplicated).
		seen := make(map[string]struct{})
		var tasks []blobTask
		for _, pi := range platInfos {
			if _, ok := seen[pi.digest]; !ok {
				seen[pi.digest] = struct{}{}
				tasks = append(tasks, blobTask{pi.digest, 0, ""}) // internal, not reported
			}
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
			if d := trimSHA256Prefix(m.Config.Digest); d != "" {
				if _, ok := seen[d]; !ok {
					seen[d] = struct{}{}
					tasks = append(tasks, blobTask{d, m.Config.Size, pi.platform})
				}
			}
			for _, layer := range m.Layers {
				if d := trimSHA256Prefix(layer.Digest); d != "" {
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
		if err := destClient.PutObject(ctx, destParsed.Bucket, destPrefix+"manifest.json", writeManifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
		if err := destClient.PutObject(ctx, destParsed.Bucket, destPrefix+"oci-layout", ociLayout); err != nil {
			return nil, fmt.Errorf("write oci-layout: %w", err)
		}

		_ = recordHistory(ctx, destClient, destParsed, writeManifestData, manifestLogicalSize(ctx, destClient, destParsed.Bucket, writeManifestData))

		return &CopyResult{
			Platforms:    len(selected),
			BlobsCopied:  int(blobsCopied.Load()),
			BlobsSkipped: int(blobsSkipped.Load()),
		}, nil
	}

	// Single-arch: collect blobs, copy in parallel.
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
		return nil, fmt.Errorf("parse source manifest: %w", err)
	}
	var tasks []blobTask
	if d := trimSHA256Prefix(manifest.Config.Digest); d != "" {
		tasks = append(tasks, blobTask{d, manifest.Config.Size, "single"})
	}
	for _, layer := range manifest.Layers {
		if d := trimSHA256Prefix(layer.Digest); d != "" {
			tasks = append(tasks, blobTask{d, layer.Size, "single"})
		}
	}
	if err := runParallel(tasks); err != nil {
		return nil, err
	}

	// Copy manifest files.
	srcManifestPrefix := srcParsed.ManifestsPrefix()
	manifestKeys, err := srcClient.ListKeys(ctx, srcParsed.Bucket, srcManifestPrefix)
	if err != nil {
		return nil, fmt.Errorf("list source manifest files: %w", err)
	}
	for _, key := range manifestKeys {
		rel := strings.TrimPrefix(key, srcManifestPrefix)
		destKey := destPrefix + rel
		if sameBucket {
			if err := destClient.CopyObject(ctx, destParsed.Bucket, key, destKey); err != nil {
				return nil, fmt.Errorf("copy manifest file %s: %w", rel, err)
			}
		} else {
			data, err := srcClient.GetObject(ctx, srcParsed.Bucket, key)
			if err != nil {
				return nil, fmt.Errorf("download manifest file %s: %w", rel, err)
			}
			if err := destClient.PutObject(ctx, destParsed.Bucket, destKey, data); err != nil {
				return nil, fmt.Errorf("upload manifest file %s: %w", rel, err)
			}
		}
	}
	_ = recordHistory(ctx, destClient, destParsed, manifestData, manifestLogicalSize(ctx, destClient, destParsed.Bucket, manifestData))

	return &CopyResult{
		Platforms:    1,
		BlobsCopied:  int(blobsCopied.Load()),
		BlobsSkipped: int(blobsSkipped.Load()),
	}, nil
}
