package image

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"encoding/json"

	"github.com/OuFinx/s3lo/v2/pkg/chunkstore"
	"github.com/OuFinx/s3lo/v2/pkg/oci"
	"github.com/OuFinx/s3lo/v2/pkg/ref"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// tagManifestFile is the single object that constitutes a tag.
//
// A tag used to be four objects (manifest.json, config.json, index.json,
// oci-layout) written in a loop, which made every tag update non-atomic: a
// reader could observe a new manifest against a stale config, and a push that
// died halfway left the tag permanently inconsistent. Nothing ever read the
// other three — every reader in this package resolves a tag through
// manifest.json and then fetches blobs by digest — and the per-tag directory was
// not a valid OCI layout anyway, because blobs/ lives at the bucket root rather
// than beside it. Collapsing a tag to one object makes the write atomic by
// construction: S3 PutObject either lands or it does not.
const tagManifestFile = "manifest.json"

// PushOptions controls push behavior.
type PushOptions struct {
	// Force overwrites an existing tag even if the bucket has immutability enabled.
	Force bool
	// OnStart is called once with the total blob bytes before any uploads begin.
	OnStart func(totalBytes int64)
	// OnBlob is called for each blob after it is processed (uploaded or skipped).
	// digest is the sha256 digest (without "sha256:" prefix), size in bytes, skipped=true if already existed.
	OnBlob func(digest string, size int64, skipped bool)
	// OnTransfer is called once after all blobs are stored, with how much of the
	// image had to be uploaded versus reused. On a chunked bucket this is where
	// sub-layer deduplication becomes visible.
	OnTransfer func(stats chunkstore.Stats)
	// Platform selects which platform to publish when the local daemon holds
	// several. Empty means the host platform.
	Platform string
	// OnPlatformsDropped is called when the local image holds platforms this
	// push is not publishing, with the one being published and the rest.
	OnPlatformsDropped func(pushed string, dropped []string)
}

// Push exports a local Docker image and uploads it to S3 using the v1.1.0 layout:
//   - blobs -> blobs/sha256/<digest>  (global, Intelligent-Tiering, cross-image dedup)
//   - manifests -> manifests/<image>/<tag>/  (Standard storage class)
func Push(ctx context.Context, imageRef, s3Ref string, opts PushOptions) error {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid S3 reference: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "s3lo-push-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// A tag can hold several platforms on a containerd image store, and push
	// publishes one. It used to pick whatever the daemon handed over and say
	// nothing, which is the asymmetry with copy that this reports.
	available, err := oci.LocalPlatforms(ctx, imageRef)
	if err != nil {
		return fmt.Errorf("read local platforms: %w", err)
	}
	platform, dropped, err := selectPushPlatform(available, opts.Platform, hostPlatform())
	if err != nil {
		return err
	}
	if len(dropped) > 0 && opts.OnPlatformsDropped != nil {
		opts.OnPlatformsDropped(platform, dropped)
	}

	_, manifestData, configData, err := oci.ExportImage(ctx, imageRef, tmpDir, platform)
	if err != nil {
		// Push stages the whole image on disk before uploading, so a large image
		// can exhaust a small temp filesystem while the destination has room to
		// spare. Say so, because "no space left on device" points at the wrong disk.
		if errors.Is(err, syscall.ENOSPC) || strings.Contains(err.Error(), "no space left on device") {
			return fmt.Errorf("export image: ran out of space staging the image under %s. "+
				"Set TMPDIR to a filesystem with room for the uncompressed image and retry: %w",
				os.TempDir(), err)
		}
		return fmt.Errorf("export image: %w", err)
	}

	// Trust the export, not the request. A classic image store accepts a
	// platform argument and hands back the one platform it has, so asking for
	// linux/arm64 on an amd64-only daemon would otherwise publish amd64 content
	// under an arm64 name and deduplicate perfectly against it.
	if opts.Platform != "" {
		if err := checkExportedPlatform(configData, opts.Platform); err != nil {
			return err
		}
	}

	if err := oci.WriteOCILayout(tmpDir, manifestData, configData); err != nil {
		return fmt.Errorf("write OCI layout: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	if err := enforceTagWritePolicy(ctx, client, parsed, opts.Force); err != nil {
		return err
	}

	// Upload blobs to global blobs/sha256/ with Intelligent-Tiering in parallel.
	blobsDir := filepath.Join(tmpDir, "blobs", "sha256")
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		return fmt.Errorf("read blobs dir: %w", err)
	}

	// Chunking is a property of the bucket, read once per push.
	cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
	if err != nil {
		return fmt.Errorf("read bucket config: %w", err)
	}
	chunked := cfg.ChunkedEnabled()
	if chunked {
		if err := ensureChunkFormat(ctx, client, parsed.Bucket, cfg); err != nil {
			return err
		}
	}

	// Sum blob sizes for deterministic progress reporting.
	if opts.OnStart != nil {
		var totalBytes int64
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if info, err := entry.Info(); err == nil {
				totalBytes += info.Size()
			}
		}
		if totalBytes > 0 {
			opts.OnStart(totalBytes)
		}
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(blobConcurrency)
	var (
		onBlobMu sync.Mutex
		total    chunkstore.Stats
		// Layers that ended up chunked, keyed by their raw digest, so the manifest
		// can be pointed at the compressed form afterwards.
		compressed = map[string]chunkstore.Recipe{}
	)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entry := entry
		g.Go(func() error {
			localPath := filepath.Join(blobsDir, entry.Name())

			info, err := os.Stat(localPath)
			if err != nil {
				return fmt.Errorf("stat blob %s: %w", entry.Name(), err)
			}

			recipe, stats, skipped, err := storeLayer(gCtx, client, parsed.Bucket, localPath,
				entry.Name(), info.Size(), chunked)
			if err != nil {
				return err
			}
			slog.Debug("stored blob", "digest", entry.Name()[:12], "size", info.Size(),
				"chunks", stats.Chunks, "uploaded", stats.BytesUploaded, "skipped", skipped)

			onBlobMu.Lock()
			if recipe.CompressedDigest != "" {
				compressed[entry.Name()] = recipe
			}
			total.Chunks += stats.Chunks
			total.ChunksUploaded += stats.ChunksUploaded
			total.Bytes += stats.Bytes
			total.BytesUploaded += stats.BytesUploaded
			if opts.OnBlob != nil {
				opts.OnBlob(entry.Name(), info.Size(), skipped)
			}
			onBlobMu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if opts.OnTransfer != nil {
		opts.OnTransfer(total)
	}

	// Point the manifest at the compressed form of every chunked layer. The image
	// config is untouched — its diff_ids are the raw digests, which is exactly what
	// the spec wants — so the image ID does not change.
	published, err := retargetManifest(manifestData, compressed)
	if err != nil {
		return err
	}

	// Publish the tag last: every blob it references is already in the bucket, so
	// this single write is what makes the image visible, all at once.
	manifestPrefix := parsed.ManifestsPrefix()
	if err := client.PutObject(ctx, parsed.Bucket, manifestPrefix+tagManifestFile, published); err != nil {
		return fmt.Errorf("publish tag: %w", err)
	}

	// Record push history (best-effort — don't fail the push on history errors).
	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				totalSize += info.Size()
			}
		}
	}

	return nil
}

// retargetManifest rewrites layer descriptors for layers stored as chunks so they
// advertise the compressed stream a client should fetch. Layers stored whole are
// left alone, which is what keeps a mixed bucket readable.
func retargetManifest(manifestData []byte, compressed map[string]chunkstore.Recipe) ([]byte, error) {
	if len(compressed) == 0 {
		return manifestData, nil
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		// An image index carries no layers of its own; nothing to retarget.
		return manifestData, nil
	}
	if len(m.Layers) == 0 {
		return manifestData, nil
	}
	changed := false
	for i, l := range m.Layers {
		r, ok := compressed[l.Digest.Encoded()]
		if !ok {
			continue
		}
		m.Layers[i].Digest = digest.Digest("sha256:" + r.CompressedDigest)
		m.Layers[i].Size = r.CompressedSize
		m.Layers[i].MediaType = ocispec.MediaTypeImageLayerZstd
		changed = true
	}
	if !changed {
		return manifestData, nil
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal retargeted manifest: %w", err)
	}
	return out, nil
}
