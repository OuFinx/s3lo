package image

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"gopkg.in/yaml.v3"
)

// LifecycleRule defines retention criteria for images matching a name pattern.
type LifecycleRule struct {
	// Match is a glob pattern matched against image names (e.g. "*", "dev/*", "myapp").
	Match string `yaml:"match"`
	// KeepLast keeps the N most recently pushed tags. 0 means no limit.
	KeepLast int `yaml:"keep_last"`
	// MaxAge deletes tags older than this duration (e.g. "7d", "30d", "90d").
	MaxAge string `yaml:"max_age"`
	// KeepTags lists tags that are never deleted regardless of other rules.
	KeepTags []string `yaml:"keep_tags"`
}

// LifecycleConfig holds a lifecycle policy loaded from a YAML file.
type LifecycleConfig struct {
	Rules []LifecycleRule `yaml:"rules"`
}

// ParseLifecycleConfig parses a lifecycle YAML config.
func ParseLifecycleConfig(data []byte) (*LifecycleConfig, error) {
	var cfg LifecycleConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse lifecycle config: %w", err)
	}
	for i, r := range cfg.Rules {
		if r.Match == "" {
			return nil, fmt.Errorf("rule %d: match pattern is required", i+1)
		}
		if r.MaxAge != "" {
			if _, err := parseDuration(r.MaxAge); err != nil {
				return nil, fmt.Errorf("rule %d: invalid max_age %q: %w", i+1, r.MaxAge, err)
			}
		}
	}
	return &cfg, nil
}

// LifecycleResult summarizes a lifecycle apply run.
type LifecycleResult struct {
	Evaluated int // tags evaluated
	Deleted   int // tags deleted (or would be in dry run)
	DryRun    bool
}

// tagMeta holds metadata about a single image tag needed for lifecycle evaluation.
type tagMeta struct {
	image       string
	tag         string
	manifestKey string
	lastModified time.Time
}

// ApplyLifecycle evaluates the lifecycle policy against all images in the bucket
// and deletes manifest files for tags that should be purged.
// If dryRun is true, no deletions are performed.
func ApplyLifecycle(ctx context.Context, s3BucketRef string, cfg *LifecycleConfig, dryRun bool) (*LifecycleResult, error) {
	bucket, prefix, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
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
		tm := tagMeta{
			image:        imageName,
			tag:          tagName,
			manifestKey:  obj.Key,
			lastModified: obj.LastModified,
		}
		imageTagsMap[imageName] = append(imageTagsMap[imageName], tm)
	}

	result := &LifecycleResult{DryRun: dryRun}

	for imageName, tags := range imageTagsMap {
		rule := matchRule(cfg.Rules, imageName)
		if rule == nil {
			continue
		}

		toDelete, err := evaluateRule(rule, tags)
		if err != nil {
			return nil, fmt.Errorf("evaluate rule for %s: %w", imageName, err)
		}

		result.Evaluated += len(tags)
		result.Deleted += len(toDelete)

		if !dryRun {
			for _, tm := range toDelete {
				// Delete all files under the tag's manifests prefix.
				tagPrefix := strings.TrimSuffix(tm.manifestKey, "manifest.json")
				keys, err := client.ListKeys(ctx, bucket, tagPrefix)
				if err != nil {
					return nil, fmt.Errorf("list tag files for %s:%s: %w", tm.image, tm.tag, err)
				}
				if err := client.DeleteObjects(ctx, bucket, keys); err != nil {
					return nil, fmt.Errorf("delete %s:%s: %w", tm.image, tm.tag, err)
				}
			}
		}
	}

	return result, nil
}

// matchRule returns the first rule whose Match pattern matches imageName, or nil.
func matchRule(rules []LifecycleRule, imageName string) *LifecycleRule {
	for i := range rules {
		matched, err := path.Match(rules[i].Match, imageName)
		if err != nil {
			continue
		}
		if matched {
			return &rules[i]
		}
	}
	return nil
}

// evaluateRule returns the tags that should be deleted according to the rule.
func evaluateRule(rule *LifecycleRule, tags []tagMeta) ([]tagMeta, error) {
	keepSet := make(map[string]bool, len(rule.KeepTags))
	for _, t := range rule.KeepTags {
		keepSet[t] = true
	}

	// Parse max_age once.
	var maxAge time.Duration
	if rule.MaxAge != "" {
		var err error
		maxAge, err = parseDuration(rule.MaxAge)
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

	var toDelete []tagMeta
	for i, tm := range sorted {
		if keepSet[tm.tag] {
			continue
		}
		shouldDelete := false

		if rule.KeepLast > 0 && i >= rule.KeepLast {
			shouldDelete = true
		}
		if maxAge > 0 && now.Sub(tm.lastModified) > maxAge {
			shouldDelete = true
		}

		if shouldDelete {
			toDelete = append(toDelete, tm)
		}
	}
	return toDelete, nil
}

// parseDuration parses durations like "7d", "30d", "90d".
// Only days are supported (e.g. "7d"). Standard Go durations (e.g. "24h") also work.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}
