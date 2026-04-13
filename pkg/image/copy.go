package image

import (
	"context"
	"strings"
)

// CopyResult summarizes a copy operation.
type CopyResult struct {
	BlobsCopied  int
	BlobsSkipped int
	Platforms    int // number of platforms copied (1 for single-arch)
}

// CopyOptions controls copy behavior.
type CopyOptions struct {
	// Platform filters to a specific platform (e.g. "linux/amd64").
	// Empty means copy all platforms.
	Platform string
	// OnStart is called once with the total bytes to transfer before any blobs are processed.
	OnStart func(totalBytes int64)
	// OnBlob is called for each content blob (config or layer) after it is processed.
	// platform is the OCI platform string (e.g. "linux/amd64") or "single" for single-arch.
	// skipped is true if the blob already existed at the destination.
	OnBlob func(platform, digest string, size int64, skipped bool)
}

// Copy copies an image from src to dest.
// src can be:
//   - s3://bucket/image:tag  (S3 source)
//   - <registry>/<image>:<tag>  (OCI registry, e.g. ECR or Docker Hub)
//
// dest must be s3://bucket/image:tag.
func Copy(ctx context.Context, src, destRef string, opts CopyOptions) (*CopyResult, error) {
	if strings.HasPrefix(src, "s3://") || strings.HasPrefix(src, "gs://") ||
		strings.HasPrefix(src, "az://") || strings.HasPrefix(src, "local://") {
		return copyS3ToS3(ctx, src, destRef, opts)
	}
	return copyRegistryToS3(ctx, src, destRef, opts)
}

// blobTask describes one blob to copy or upload.
// platform="" marks internal blobs (e.g. platform manifest JSON) that are not
// reported via OnBlob and do not count toward progress.
type blobTask struct {
	digest   string
	size     int64
	platform string
}
