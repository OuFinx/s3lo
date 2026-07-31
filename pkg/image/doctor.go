package image

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/OuFinx/s3lo/v3/pkg/chunkstore"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/sync/errgroup"
)

// Intelligent-Tiering advisory states reported by Doctor for s3:// buckets.
const (
	TieringEnabled       = "enabled"
	TieringNotConfigured = "not configured"
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
	ConfigPresent  bool          `json:"config_present"`
	ManifestIssues []DoctorIssue `json:"manifest_issues,omitempty"`
	OrphanedBlobs  int           `json:"orphaned_blobs"`
	OrphanedBytes  int64         `json:"orphaned_bytes"`
	// IntelligentTiering is only meaningful for s3:// buckets; empty elsewhere.
	IntelligentTiering string `json:"intelligent_tiering,omitempty"`
}

// Doctor performs a health check on the given S3 bucket and returns findings.
// It checks layout structure, manifest integrity (all referenced blobs exist),
// orphaned blobs, and config validity.
func Doctor(ctx context.Context, s3BucketRef string) (*DoctorResult, error) {
	bucket, nameFilter, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	scheme := schemeOf(s3BucketRef)
	result := &DoctorResult{Bucket: bucket, Scheme: scheme}

	// --- Reachability check ---
	// A local directory that does not exist lists as empty rather than failing,
	// so without this probe doctor would report "no layout" for a typo'd path.
	if scheme == "local://" {
		if _, err := os.Stat(bucket); err != nil {
			return nil, fmt.Errorf("cannot reach %s%s: %w", scheme, bucket, err)
		}
	} else {
		// Probe through the backend the caller actually configured, with a prefix
		// that matches nothing: cheap on every backend, and still fails when the
		// bucket is missing or the credentials are wrong.
		//
		// This used to call GetBucketLocation on a freshly built AWS client, which
		// was wrong twice over: it ignored --endpoint, and GetBucketLocation is an
		// AWS-only API. MinIO, R2 and Ceph answer it with 403 — the very backends
		// --endpoint exists to reach — so doctor failed on a bucket that push,
		// list, cat and stats were all using happily.
		if _, err := client.ListKeys(ctx, bucket, reachabilityProbePrefix); err != nil {
			return nil, fmt.Errorf("cannot reach %s%s — check the bucket name, region, and credentials: %w", scheme, bucket, err)
		}
	}

	// Intelligent-Tiering is an AWS-only advisory. It must never decide whether a
	// bucket is healthy, so every failure here is silent.
	if scheme == "s3://" {
		result.IntelligentTiering, _ = checkS3Tiering(ctx, bucket)
	}

	// --- Layout check ---
	// The integrity check honours the ref's image-name filter; the orphan check
	// below cannot, because a blob is only an orphan when *no* image in the
	// bucket references it.
	manifestKeys, err := client.ListKeys(ctx, bucket, scopedManifests(nameFilter))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	allManifestKeys := manifestKeys
	if nameFilter != "" {
		allManifestKeys, err = client.ListKeys(ctx, bucket, manifestsRoot)
		if err != nil {
			return nil, fmt.Errorf("list manifests: %w", err)
		}
	}
	blobMeta, err := client.ListObjectsWithMeta(ctx, bucket, "blobs/sha256/")
	if err != nil {
		return nil, fmt.Errorf("list blobs: %w", err)
	}
	result.LayoutOK = len(manifestKeys) > 0 || len(blobMeta) > 0

	// A layer counts as present if the bucket holds it whole OR holds a recipe
	// that rebuilds it from chunks. Without the second source every chunked layer
	// looks missing and doctor tells the operator to delete healthy images.
	recipeMeta, err := client.ListObjectsWithMeta(ctx, bucket, chunkstore.RecipesPrefix)
	if err != nil {
		return nil, fmt.Errorf("list recipes: %w", err)
	}

	storedBlobs := make(map[string]int64, len(blobMeta)+len(recipeMeta))
	for _, b := range blobMeta {
		digest := b.Key[strings.LastIndex(b.Key, "/")+1:]
		storedBlobs[digest] = b.Size
	}
	for _, r := range recipeMeta {
		digest := r.Key[strings.LastIndex(r.Key, "/")+1:]
		if _, ok := storedBlobs[digest]; !ok {
			storedBlobs[digest] = r.Size
		}
	}

	// --- Config check ---
	// An absent s3lo.yaml is fine (config is optional); only a config that exists
	// but cannot be read is an actual problem.
	_, cfgErr := client.GetObject(ctx, bucket, bucketConfigKey)
	switch {
	case cfgErr == nil:
		result.ConfigPresent = true
		result.ConfigOK = true
	case storage.IsNotFound(cfgErr):
		result.ConfigPresent = false
		result.ConfigOK = true
	default:
		result.ConfigPresent = false
		result.ConfigOK = false
	}

	// --- Manifest integrity check ---
	// For each manifest.json, verify all referenced blobs exist.
	var manifestJsonKeys []string
	for _, k := range manifestKeys {
		if strings.HasSuffix(k, "/manifest.json") {
			manifestJsonKeys = append(manifestJsonKeys, k)
		}
	}

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
				rel := strings.TrimPrefix(key, manifestsRoot)
				rel = strings.TrimSuffix(rel, "/manifest.json")
				mu.Lock()
				issues = append(issues, DoctorIssue{
					Image:   imageTagFromManifestKey(rel),
					Message: fmt.Sprintf("cannot read manifest: %v", err),
				})
				mu.Unlock()
				return nil
			}

			rel := strings.TrimPrefix(key, manifestsRoot)
			rel = strings.TrimSuffix(rel, "/manifest.json")
			imageTag := imageTagFromManifestKey(rel)

			refs, walkErr := collectManifestReferences(gCtx, client, bucket, data)
			var missing []string
			for digest := range refs {
				if _, ok := storedBlobs[digest]; !ok {
					missing = append(missing, "sha256:"+short(digest)+"...")
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
	// Collect all blob digests referenced by all parseable manifests. This walks
	// every manifest in the bucket even when the ref narrowed the checks above:
	// blobs are shared, so a blob is only orphaned when nothing at all wants it.
	referenced := make(map[string]struct{})
	for _, key := range allManifestKeys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
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

// reachabilityProbePrefix matches no real key, so listing it is the cheapest
// call that still proves the bucket exists and the credentials work.
const reachabilityProbePrefix = ".s3lo-reachability-probe/"

// schemeOf returns the ref's URL scheme. Defaulting everything non-local to
// "s3://" made doctor label gs:// and az:// buckets as S3 in both its text and
// its JSON output.
func schemeOf(ref string) string {
	for _, s := range []string{"local://", "gs://", "az://", "s3://"} {
		if strings.HasPrefix(ref, s) {
			return s
		}
	}
	return "s3://"
}

// checkS3Tiering reports whether S3 Intelligent-Tiering is configured. It is
// advisory only: every failure, including a missing permission or a backend
// that does not implement the API, returns no opinion rather than an error.
func checkS3Tiering(ctx context.Context, bucket string) (string, error) {
	client, err := storage.NewS3Client(ctx)
	if err != nil {
		return "", nil
	}
	s3c, err := client.ClientForBucket(ctx, bucket)
	if err != nil {
		return "", nil
	}
	resp, err := s3c.ListBucketIntelligentTieringConfigurations(ctx,
		&s3.ListBucketIntelligentTieringConfigurationsInput{Bucket: &bucket})
	if err != nil {
		return "", nil
	}
	if len(resp.IntelligentTieringConfigurationList) > 0 {
		return TieringEnabled, nil
	}
	return TieringNotConfigured, nil
}

// imageTagFromManifestKey converts a relative path like "myapp/v1.0" to "myapp:v1.0".
func imageTagFromManifestKey(rel string) string {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return rel
	}
	return rel[:i] + ":" + rel[i+1:]
}
