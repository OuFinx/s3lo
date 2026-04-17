package image

import (
	"context"
	"fmt"
	"strings"
	"sync"

	storage "github.com/OuFinx/s3lo/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// DoctorIssue describes a single problem found during a bucket health check.
type DoctorIssue struct {
	Image   string `json:"image"`
	Message string `json:"message"`
}

// DoctorResult holds the findings of a bucket health check.
type DoctorResult struct {
	Bucket         string        `json:"bucket"`
	Scheme         string        `json:"scheme"`
	LayoutOK       bool          `json:"layout_ok"`
	ConfigOK       bool          `json:"config_ok"`
	ManifestIssues []DoctorIssue `json:"manifest_issues,omitempty"`
	OrphanedBlobs  int           `json:"orphaned_blobs"`
	OrphanedBytes  int64         `json:"orphaned_bytes"`
}

// Doctor performs a health check on the given S3 bucket and returns findings.
// It checks layout structure, manifest integrity (all referenced blobs exist),
// orphaned blobs, and config validity.
func Doctor(ctx context.Context, s3BucketRef string) (*DoctorResult, error) {
	bucket, prefix, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	scheme := "s3://"
	if strings.HasPrefix(s3BucketRef, "local://") {
		scheme = "local://"
	}
	result := &DoctorResult{Bucket: bucket, Scheme: scheme}

	// --- Layout check ---
	manifestKeys, err := client.ListKeys(ctx, bucket, prefix+"manifests/")
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	blobMeta, err := client.ListObjectsWithMeta(ctx, bucket, prefix+"blobs/sha256/")
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}
	result.LayoutOK = len(manifestKeys) > 0 || len(blobMeta) > 0

	// Build set of all stored blob digests.
	storedBlobs := make(map[string]int64, len(blobMeta))
	for _, b := range blobMeta {
		digest := b.Key[strings.LastIndex(b.Key, "/")+1:]
		storedBlobs[digest] = b.Size
	}

	// --- Config check ---
	_, cfgErr := client.GetObject(ctx, bucket, prefix+bucketConfigKey)
	result.ConfigOK = cfgErr == nil

	// --- Manifest integrity check ---
	// For each manifest.json, verify all referenced blobs exist.
	var manifestJsonKeys []string
	for _, k := range manifestKeys {
		if strings.HasSuffix(k, "/manifest.json") {
			manifestJsonKeys = append(manifestJsonKeys, k)
		}
	}

	manifestsPrefix := prefix + "manifests/"
	var (
		mu     sync.Mutex
		issues []DoctorIssue
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(scanConcurrency)

	for _, key := range manifestJsonKeys {
		key := key
		g.Go(func() error {
			data, err := client.GetObject(gCtx, bucket, key)
			if err != nil {
				rel := strings.TrimPrefix(key, manifestsPrefix)
				rel = strings.TrimSuffix(rel, "/manifest.json")
				mu.Lock()
				issues = append(issues, DoctorIssue{
					Image:   imageTagFromManifestKey(rel),
					Message: fmt.Sprintf("cannot read manifest: %v", err),
				})
				mu.Unlock()
				return nil
			}

			rel := strings.TrimPrefix(key, manifestsPrefix)
			rel = strings.TrimSuffix(rel, "/manifest.json")
			imageTag := imageTagFromManifestKey(rel)

			refs, walkErr := collectManifestReferences(gCtx, client, bucket, data)
			var missing []string
			for digest := range refs {
				if _, ok := storedBlobs[digest]; !ok {
					missing = append(missing, "sha256:"+digest[:12]+"...")
				}
			}

			if len(missing) > 0 || walkErr != nil {
				msg := ""
				if len(missing) > 0 {
					msg = fmt.Sprintf("missing blob(s): %s", strings.Join(missing, ", "))
				}
				if walkErr != nil {
					if msg != "" {
						msg += "; "
					}
					msg += walkErr.Error()
				}
				mu.Lock()
				issues = append(issues, DoctorIssue{
					Image:   imageTag,
					Message: fmt.Sprintf("%s (image cannot be repaired — delete with: s3lo delete %s%s/%s)", msg, scheme, bucket, imageTag),
				})
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	result.ManifestIssues = issues

	// --- Orphaned blob check ---
	// Collect all blob digests referenced by all parseable manifests.
	referenced := make(map[string]struct{})
	for _, key := range manifestJsonKeys {
		data, err := client.GetObject(ctx, bucket, key)
		if err != nil {
			continue
		}
		refs, err := collectManifestReferences(ctx, client, bucket, data)
		for digest := range refs {
			referenced[digest] = struct{}{}
		}
		if err != nil {
			continue
		}
	}

	for digest, size := range storedBlobs {
		if _, ok := referenced[digest]; !ok {
			result.OrphanedBlobs++
			result.OrphanedBytes += size
		}
	}

	return result, nil
}

// imageTagFromManifestKey converts a relative path like "myapp/v1.0" to "myapp:v1.0".
func imageTagFromManifestKey(rel string) string {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return rel
	}
	return rel[:i] + ":" + rel[i+1:]
}
