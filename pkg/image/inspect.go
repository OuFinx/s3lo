package image

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// ImageInfo holds metadata about an image stored on S3.
type ImageInfo struct {
	Reference string           `json:"reference" yaml:"reference"`
	IsIndex   bool             `json:"is_index" yaml:"is_index"`
	// Single-arch fields (IsIndex == false).
	Manifest  ocispec.Manifest `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Layers    []LayerDetail    `json:"layers,omitempty" yaml:"layers,omitempty"`
	TotalSize int64            `json:"total_size,omitempty" yaml:"total_size,omitempty"`
	// Multi-arch fields (IsIndex == true).
	Platforms []PlatformInfo   `json:"platforms,omitempty" yaml:"platforms,omitempty"`
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

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	// Try v1.1.0 layout first.
	key := parsed.ManifestsPrefix() + "manifest.json"
	data, err := client.GetObject(ctx, parsed.Bucket, key)
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if !errors.As(err, &noSuchKey) {
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
		g.SetLimit(10)
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

