package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3client "github.com/finx/s3lo/pkg/s3"
)

// ImageEntry represents an image and its available tags in the registry.
type ImageEntry struct {
	Name string
	Tags []string
}

// List lists all images in an S3 bucket path.
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

	// List top-level prefixes (image names)
	imageNames, err := listCommonPrefixes(ctx, s3c, bucket, prefix, "/")
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}

	var entries []ImageEntry
	for _, imagePfx := range imageNames {
		// imagePfx looks like "prefix/imagename/" — extract just the image name
		name := strings.TrimPrefix(imagePfx, prefix)
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

		entries = append(entries, ImageEntry{Name: name, Tags: tags})
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
