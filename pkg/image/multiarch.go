package image

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	godigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// ManifestCreateResult summarizes a manifest create operation.
type ManifestCreateResult struct {
	Platforms int
}

// ManifestCreate creates a multi-arch OCI Image Index from existing single-arch tags in S3.
// All srcRefs must be in the same bucket as destRef.
func ManifestCreate(ctx context.Context, destRef string, srcRefs []string) (*ManifestCreateResult, error) {
	destParsed, err := ref.Parse(destRef)
	if err != nil {
		return nil, fmt.Errorf("invalid destination reference: %w", err)
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	var descriptors []ocispec.Descriptor

	for _, srcRef := range srcRefs {
		srcParsed, err := ref.Parse(srcRef)
		if err != nil {
			return nil, fmt.Errorf("invalid source reference %q: %w", srcRef, err)
		}
		if srcParsed.Bucket != destParsed.Bucket {
			return nil, fmt.Errorf("all sources must be in the same bucket as the destination (got %q and %q)", srcParsed.Bucket, destParsed.Bucket)
		}

		// Read the source manifest.
		manifestKey := srcParsed.ManifestsPrefix() + "manifest.json"
		manifestData, err := client.GetObject(ctx, srcParsed.Bucket, manifestKey)
		if err != nil {
			return nil, fmt.Errorf("fetch manifest for %q: %w", srcRef, err)
		}
		if isImageIndex(manifestData) {
			return nil, fmt.Errorf("source %q is already a multi-arch image — use single-arch tags as sources", srcRef)
		}

		// Compute manifest digest and size, store it as a blob.
		h := sha256.Sum256(manifestData)
		dgst := godigest.NewDigestFromEncoded(godigest.SHA256, fmt.Sprintf("%x", h))
		encoded := dgst.Encoded()

		exists, _ := client.HeadObjectExists(ctx, destParsed.Bucket, "blobs/sha256/"+encoded)
		if !exists {
			if err := client.PutObject(ctx, destParsed.Bucket, "blobs/sha256/"+encoded, manifestData); err != nil {
				return nil, fmt.Errorf("store manifest blob for %q: %w", srcRef, err)
			}
		}

		// Read the image config to determine the platform.
		var m struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if err := json.Unmarshal(manifestData, &m); err != nil {
			return nil, fmt.Errorf("parse manifest for %q: %w", srcRef, err)
		}
		configData, err := client.GetObject(ctx, srcParsed.Bucket, "blobs/sha256/"+trimSHA256Prefix(m.Config.Digest))
		if err != nil {
			return nil, fmt.Errorf("fetch config for %q: %w", srcRef, err)
		}
		platform, err := platformFromConfig(configData)
		if err != nil {
			return nil, fmt.Errorf("read platform from config for %q: %w", srcRef, err)
		}

		descriptors = append(descriptors, ocispec.Descriptor{
			MediaType: mediaTypeOCIManifest,
			Digest:    dgst,
			Size:      int64(len(manifestData)),
			Platform:  platform,
		})
	}

	// Build and write the OCI Image Index.
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: mediaTypeOCIIndex,
		Manifests: descriptors,
	}
	indexData, err := json.Marshal(index)
	if err != nil {
		return nil, fmt.Errorf("marshal image index: %w", err)
	}

	destPrefix := destParsed.ManifestsPrefix()
	if err := client.PutObject(ctx, destParsed.Bucket, destPrefix+"manifest.json", indexData); err != nil {
		return nil, fmt.Errorf("write manifest.json: %w", err)
	}
	ociLayout := []byte(`{"imageLayoutVersion":"1.0.0"}`)
	if err := client.PutObject(ctx, destParsed.Bucket, destPrefix+"oci-layout", ociLayout); err != nil {
		return nil, fmt.Errorf("write oci-layout: %w", err)
	}

	return &ManifestCreateResult{Platforms: len(descriptors)}, nil
}

// platformFromConfig extracts OS and architecture from an OCI image config blob.
func platformFromConfig(data []byte) (*ocispec.Platform, error) {
	var cfg struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.OS == "" {
		cfg.OS = "linux"
	}
	if cfg.Architecture == "" {
		cfg.Architecture = "amd64"
	}
	return &ocispec.Platform{
		OS:           cfg.OS,
		Architecture: cfg.Architecture,
		Variant:      cfg.Variant,
	}, nil
}

// mustParseDigest parses a digest string, panicking on error (only for known-valid digests).
func mustParseDigest(s string) godigest.Digest {
	return godigest.Digest(s)
}
