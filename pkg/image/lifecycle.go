package image

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

// LifecycleResult summarizes a lifecycle apply run.
type LifecycleResult struct {
	Evaluated int
	Deleted   int
	DryRun    bool
}

// tagMeta holds metadata about a single image tag needed for lifecycle evaluation.
type tagMeta struct {
	image        string
	tag          string
	manifestKey  string
	lastModified time.Time
}

// LoadBucketConfigFromFile parses a BucketConfig from a local YAML file's bytes.
func LoadBucketConfigFromFile(data []byte) (*BucketConfig, error) {
	var cfg BucketConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Validate lifecycle durations.
	validate := func(lc *LifecycleImageConfig) error {
		if lc != nil && lc.MaxAge != "" {
			if _, err := ParseDuration(lc.MaxAge); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validate(cfg.Default.Lifecycle); err != nil {
		return nil, fmt.Errorf("default lifecycle: %w", err)
	}
	for name, img := range cfg.Images {
		if err := validate(img.Lifecycle); err != nil {
			return nil, fmt.Errorf("image %q lifecycle: %w", name, err)
		}
	}
	return &cfg, nil
}

// ApplyLifecycle evaluates the lifecycle settings in cfg against all images in the bucket
// and deletes manifest files for tags that should be purged.
// If dryRun is true, no deletions are performed.
func ApplyLifecycle(ctx context.Context, s3BucketRef string, cfg *BucketConfig, dryRun bool) (*LifecycleResult, error) {
	bucket, prefix, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := s3client.NewBackendFromRef(ctx, s3BucketRef)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	// Collect all tags with their LastModified time.
	manifestsPrefix := prefix + "manifests/"
	objects, err := client.ListObjectsWithMeta(ctx, bucket, manifestsPrefix)
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}

	// Group tags by image name.
	imageTagsMap := make(map[string][]tagMeta)
	for _, obj := range objects {
		if !strings.HasSuffix(obj.Key, "/manifest.json") {
			continue
		}
		rel := strings.TrimPrefix(obj.Key, manifestsPrefix)
		rel = strings.TrimSuffix(rel, "/manifest.json")
		lastSlash := strings.LastIndex(rel, "/")
		if lastSlash < 0 {
			continue
		}
		imageName := rel[:lastSlash]
		tagName := rel[lastSlash+1:]
		imageTagsMap[imageName] = append(imageTagsMap[imageName], tagMeta{
			image:        imageName,
			tag:          tagName,
			manifestKey:  obj.Key,
			lastModified: obj.LastModified,
		})
	}

	result := &LifecycleResult{DryRun: dryRun}

	for imageName, tags := range imageTagsMap {
		lc := cfg.EffectiveConfig(imageName).Lifecycle
		if lc == nil || (lc.KeepLast == 0 && lc.MaxAge == "" && len(lc.KeepTags) == 0) {
			continue // no lifecycle policy for this image
		}

		toDelete, err := evaluateTags(lc, tags)
		if err != nil {
			return nil, fmt.Errorf("evaluate lifecycle for %s: %w", imageName, err)
		}

		result.Evaluated += len(tags)
		result.Deleted += len(toDelete)

		if !dryRun {
			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(10)
			for _, tm := range toDelete {
				tm := tm
				g.Go(func() error {
					tagPrefix := strings.TrimSuffix(tm.manifestKey, "manifest.json")
					keys, err := client.ListKeys(gCtx, bucket, tagPrefix)
					if err != nil {
						return fmt.Errorf("list tag files for %s:%s: %w", tm.image, tm.tag, err)
					}
					if err := client.DeleteObjects(gCtx, bucket, keys); err != nil {
						return fmt.Errorf("delete %s:%s: %w", tm.image, tm.tag, err)
					}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

// evaluateTags returns the tags that should be deleted according to the lifecycle config.
func evaluateTags(lc *LifecycleImageConfig, tags []tagMeta) ([]tagMeta, error) {
	keepSet := make(map[string]bool, len(lc.KeepTags))
	for _, t := range lc.KeepTags {
		keepSet[t] = true
	}

	var maxAge time.Duration
	if lc.MaxAge != "" {
		var err error
		maxAge, err = ParseDuration(lc.MaxAge)
		if err != nil {
			return nil, err
		}
	}

	now := time.Now()

	// Sort tags newest-first for keep_last evaluation.
	sorted := make([]tagMeta, len(tags))
	copy(sorted, tags)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].lastModified.After(sorted[j].lastModified)
	})

	// kept counts non-protected tags seen so far (for keep_last).
	// Protected tags are excluded from this count so they don't consume
	// slots that would cause other tags to be incorrectly deleted.
	kept := 0
	var toDelete []tagMeta
	for _, tm := range sorted {
		if keepSet[tm.tag] {
			continue
		}
		shouldDelete := false
		if lc.KeepLast > 0 && kept >= lc.KeepLast {
			shouldDelete = true
		}
		if maxAge > 0 && now.Sub(tm.lastModified) > maxAge {
			shouldDelete = true
		}
		if shouldDelete {
			toDelete = append(toDelete, tm)
		} else {
			kept++
		}
	}
	return toDelete, nil
}

// ParseDuration parses durations like "7d", "30d". Standard Go durations (e.g. "24h") also work.
func ParseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}
