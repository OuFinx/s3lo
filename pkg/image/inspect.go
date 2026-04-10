package image

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ImageInfo holds metadata about an image stored on S3.
type ImageInfo struct {
	Reference string
	Manifest  ocispec.Manifest
	Layers    []LayerDetail
	TotalSize int64
}

// LayerDetail describes a single image layer.
type LayerDetail struct {
	Digest    string
	Size      int64
	MediaType string
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
	s3c, err := client.ClientForBucket(ctx, parsed.Bucket)
	if err != nil {
		return nil, fmt.Errorf("get S3 client for bucket: %w", err)
	}

	// Try v1.1.0 layout first.
	key := parsed.ManifestsPrefix() + "manifest.json"
	data, err := getObject(ctx, s3c, parsed.Bucket, key)
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if !errors.As(err, &noSuchKey) {
			return nil, fmt.Errorf("download manifest: %w", err)
		}
		// Fall back to v1.0.0 layout.
		key = parsed.S3Prefix() + "/manifest.json"
		data, err = getObject(ctx, s3c, parsed.Bucket, key)
		if err != nil {
			return nil, fmt.Errorf("download manifest: %w", err)
		}
	}

	manifest, err := oci.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	info := &ImageInfo{
		Reference: s3Ref,
		Manifest:  manifest,
	}

	for _, layer := range manifest.Layers {
		info.Layers = append(info.Layers, LayerDetail{
			Digest:    layer.Digest.String(),
			Size:      layer.Size,
			MediaType: string(layer.MediaType),
		})
		info.TotalSize += layer.Size
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

func getObject(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
