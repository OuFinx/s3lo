package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// ImageEntry represents an image and its available tags in the registry.
type ImageEntry struct {
	Name string
	Tags []string
}

// List lists all images in an S3 bucket path.
// Supports both v1.1.0 (manifests/ prefix) and v1.0.0 (per-tag root) layouts.
// s3Ref should be like "s3://my-bucket/" or "s3://my-bucket/prefix/".
func List(ctx context.Context, s3Ref string) ([]ImageEntry, error) {
	if !strings.HasPrefix(s3Ref, "s3://") {
		return nil, fmt.Errorf("invalid s3 reference %q: must start with s3://", s3Ref)
	}

	rest := strings.TrimPrefix(s3Ref, "s3://")
	slashIdx := strings.Index(rest, "/")
	var bucket, prefix string
	if slashIdx < 0 {
		bucket = rest
		prefix = ""
	} else {
		bucket = rest[:slashIdx]
		prefix = rest[slashIdx+1:]
	}

	if bucket == "" {
		return nil, fmt.Errorf("invalid s3 reference %q: empty bucket", s3Ref)
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}
	s3c, err := client.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("get S3 client for bucket: %w", err)
	}

	// v1.1.0: images under manifests/<image>/<tag>/
	v110Entries, err := listFromManifestsPrefix(ctx, s3c, bucket, prefix+"manifests/", prefix)
	if err != nil {
		return nil, err
	}

	// v1.0.0: images at <image>/<tag>/ (skip blobs/ and manifests/ reserved prefixes)
	v100Entries, err := listFromRootPrefix(ctx, s3c, bucket, prefix)
	if err != nil {
		return nil, err
	}

	// Merge, deduplicating by name (v1.1.0 takes precedence).
	seen := make(map[string]bool)
	var entries []ImageEntry
	for _, e := range v110Entries {
		seen[e.Name] = true
		entries = append(entries, e)
	}
	for _, e := range v100Entries {
		if !seen[e.Name] {
			entries = append(entries, e)
		}
	}

	return entries, nil
}

func listFromManifestsPrefix(ctx context.Context, s3c *s3.Client, bucket, v110Prefix, userPrefix string) ([]ImageEntry, error) {
	imageNames, err := listCommonPrefixes(ctx, s3c, bucket, v110Prefix, "/")
	if err != nil {
		return nil, fmt.Errorf("list v1.1.0 images: %w", err)
	}

	var entries []ImageEntry
	for _, imagePfx := range imageNames {
		name := strings.TrimPrefix(imagePfx, v110Prefix)
		name = strings.TrimSuffix(name, "/")

		tagPrefixes, err := listCommonPrefixes(ctx, s3c, bucket, imagePfx, "/")
		if err != nil {
			return nil, fmt.Errorf("list tags for %s: %w", name, err)
		}

		var tags []string
		for _, tagPfx := range tagPrefixes {
			tag := strings.TrimPrefix(tagPfx, imagePfx)
			tag = strings.TrimSuffix(tag, "/")
			tags = append(tags, tag)
		}
		if len(tags) > 0 {
			entries = append(entries, ImageEntry{Name: name, Tags: tags})
		}
	}
	return entries, nil
}

func listFromRootPrefix(ctx context.Context, s3c *s3.Client, bucket, prefix string) ([]ImageEntry, error) {
	imageNames, err := listCommonPrefixes(ctx, s3c, bucket, prefix, "/")
	if err != nil {
		return nil, fmt.Errorf("list v1.0.0 images: %w", err)
	}

	var entries []ImageEntry
	for _, imagePfx := range imageNames {
		name := strings.TrimPrefix(imagePfx, prefix)
		name = strings.TrimSuffix(name, "/")

		// Skip v1.1.0 reserved prefixes.
		if name == "blobs" || name == "manifests" {
			continue
		}

		tagPrefixes, err := listCommonPrefixes(ctx, s3c, bucket, imagePfx, "/")
		if err != nil {
			return nil, fmt.Errorf("list tags for %s: %w", name, err)
		}

		var tags []string
		for _, tagPfx := range tagPrefixes {
			tag := strings.TrimPrefix(tagPfx, imagePfx)
			tag = strings.TrimSuffix(tag, "/")
			tags = append(tags, tag)
		}
		if len(tags) > 0 {
			entries = append(entries, ImageEntry{Name: name, Tags: tags})
		}
	}
	return entries, nil
}

func listCommonPrefixes(ctx context.Context, client *s3.Client, bucket, prefix, delimiter string) ([]string, error) {
	var prefixes []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket:    &bucket,
		Prefix:    &prefix,
		Delimiter: &delimiter,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, cp := range page.CommonPrefixes {
			if cp.Prefix != nil {
				prefixes = append(prefixes, *cp.Prefix)
			}
		}
	}
	return prefixes, nil
}
