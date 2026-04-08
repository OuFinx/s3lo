package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
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

	key := parsed.S3Prefix() + "/manifest.json"
	data, err := downloadObject(ctx, s3c, parsed.Bucket, key)
	if err != nil {
		return nil, fmt.Errorf("download manifest: %w", err)
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

func downloadObject(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
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
