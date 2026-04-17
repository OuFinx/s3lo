package image

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// ImageInfo holds metadata about an image stored on S3.
type ImageInfo struct {
	Reference string           `json:"reference" yaml:"reference"`
	IsIndex   bool             `json:"is_index" yaml:"is_index"`
	// Single-arch fields (IsIndex == false).
	Manifest  ocispec.Manifest `json:"-" yaml:"-"`
	Layers    []LayerDetail    `json:"layers,omitempty" yaml:"layers,omitempty"`
	TotalSize  int64           `json:"total_size,omitempty" yaml:"total_size,omitempty"`
	Signatures []SignatureInfo `json:"signatures,omitempty" yaml:"signatures,omitempty"`
	// Multi-arch fields (IsIndex == true).
	Platforms []PlatformInfo   `json:"platforms,omitempty" yaml:"platforms,omitempty"`
}

// SignatureInfo describes a stored signature for an image.
type SignatureInfo struct {
	KeyRef   string `json:"key_ref" yaml:"key_ref"`
	KeyID    string `json:"key_id" yaml:"key_id"`
	SignedAt string `json:"signed_at" yaml:"signed_at"`
}

// LayerDetail describes a single image layer.
type LayerDetail struct {
	Digest    string `json:"digest" yaml:"digest"`
	Size      int64  `json:"size" yaml:"size"`
	MediaType string `json:"media_type" yaml:"media_type"`
}

// PlatformInfo holds metadata for one platform in a multi-arch image.
type PlatformInfo struct {
	Platform  string        `json:"platform" yaml:"platform"`
	Digest    string        `json:"digest" yaml:"digest"`
	Layers    []LayerDetail `json:"layers,omitempty" yaml:"layers,omitempty"`
	TotalSize int64         `json:"total_size" yaml:"total_size"`
}

// Inspect fetches and returns metadata about an image on S3.
// Supports both v1.1.0 (manifests/ prefix) and v1.0.0 (per-tag) layouts.
func Inspect(ctx context.Context, s3Ref string) (*ImageInfo, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid S3 reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	// Try v1.1.0 layout first.
	key := parsed.ManifestsPrefix() + "manifest.json"
	data, err := client.GetObject(ctx, parsed.Bucket, key)
	if err != nil {
		if !storage.IsNotFound(err) {
			return nil, fmt.Errorf("download manifest: %w", err)
		}
		// Fall back to v1.0.0 layout.
		key = parsed.S3Prefix() + "/manifest.json"
		data, err = client.GetObject(ctx, parsed.Bucket, key)
		if err != nil {
			return nil, fmt.Errorf("download manifest: %w", err)
		}
	}

	info := &ImageInfo{Reference: s3Ref}

	if isImageIndex(data) {
		info.IsIndex = true
		idx, err := parseIndex(data)
		if err != nil {
			return nil, fmt.Errorf("parse image index: %w", err)
		}

		// Fetch all platform manifests in parallel.
		platforms := make([]PlatformInfo, len(idx.Manifests))
		var mu sync.Mutex
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(blobConcurrency)
		for i, desc := range idx.Manifests {
			i, desc := i, desc
			g.Go(func() error {
				platManifestData, err := client.GetObject(gCtx, parsed.Bucket, "blobs/sha256/"+desc.Digest.Encoded())
				if err != nil {
					return fmt.Errorf("fetch platform manifest %s: %w", platformString(desc.Platform), err)
				}
				platManifest, err := oci.ParseManifest(platManifestData)
				if err != nil {
					return fmt.Errorf("parse platform manifest: %w", err)
				}
				pi := PlatformInfo{
					Platform: platformString(desc.Platform),
					Digest:   desc.Digest.String(),
				}
				for _, layer := range platManifest.Layers {
					pi.Layers = append(pi.Layers, LayerDetail{
						Digest:    layer.Digest.String(),
						Size:      layer.Size,
						MediaType: string(layer.MediaType),
					})
					pi.TotalSize += layer.Size
				}
				mu.Lock()
				platforms[i] = pi
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		info.Platforms = platforms
	} else {
		manifest, err := oci.ParseManifest(data)
		if err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		info.Manifest = manifest
		for _, layer := range manifest.Layers {
			info.Layers = append(info.Layers, LayerDetail{
				Digest:    layer.Digest.String(),
				Size:      layer.Size,
				MediaType: string(layer.MediaType),
			})
			info.TotalSize += layer.Size
		}
	}

	// Load signatures (best-effort — absent signatures dir is not an error).
	sigPrefix := parsed.ManifestsPrefix() + "signatures/"
	sigKeys, _ := client.ListKeys(ctx, parsed.Bucket, sigPrefix)
	for _, sk := range sigKeys {
		if !strings.HasSuffix(sk, ".json") {
			continue
		}
		sigData, err := client.GetObject(ctx, parsed.Bucket, sk)
		if err != nil {
			continue
		}
		var rec SignatureRecord
		if err := json.Unmarshal(sigData, &rec); err != nil {
			continue
		}
		info.Signatures = append(info.Signatures, SignatureInfo{
			KeyRef:   rec.KeyRef,
			KeyID:    rec.KeyID,
			SignedAt: rec.SignedAt,
		})
	}

	return info, nil
}

// FormatJSON returns the ImageInfo as a pretty-printed JSON string.
func (i *ImageInfo) FormatJSON() (string, error) {
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal image info: %w", err)
	}
	return string(b), nil
}

