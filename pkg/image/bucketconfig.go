package image

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	storage "github.com/OuFinx/s3lo/pkg/storage"
	"gopkg.in/yaml.v3"
)

const bucketConfigKey = "s3lo.yaml"

// LifecycleImageConfig holds lifecycle retention settings for an image.
type LifecycleImageConfig struct {
	KeepLast int      `yaml:"keep_last,omitempty" json:"keep_last,omitempty"`
	MaxAge   string   `yaml:"max_age,omitempty" json:"max_age,omitempty"`
	KeepTags []string `yaml:"keep_tags,omitempty" json:"keep_tags,omitempty"`
}

// ImageConfig holds per-image s3lo configuration.
// All fields are pointers so we can distinguish "not set" from zero/false.
type ImageConfig struct {
	Immutable *bool                 `yaml:"immutable,omitempty" json:"immutable,omitempty"`
	Lifecycle *LifecycleImageConfig `yaml:"lifecycle,omitempty" json:"lifecycle,omitempty"`
}

// BucketConfig holds the full s3lo configuration for a bucket, stored at s3://bucket/s3lo.yaml.
// Default applies to all images. Images contains per-image overrides keyed by name or glob pattern.
type BucketConfig struct {
	Default  ImageConfig            `yaml:"default,omitempty" json:"default,omitempty"`
	Images   map[string]ImageConfig `yaml:"images,omitempty" json:"images,omitempty"`
	Policies []PolicyRule           `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// PolicyCheck identifies the kind of check a policy performs.
type PolicyCheck string

const (
	PolicyCheckScan   PolicyCheck = "scan"
	PolicyCheckAge    PolicyCheck = "age"
	PolicyCheckSigned PolicyCheck = "signed"
	PolicyCheckSize   PolicyCheck = "size"
)

// PolicyRule is a single policy check stored in s3lo.yaml under the `policies` key.
type PolicyRule struct {
	Name string      `yaml:"name" json:"name"`
	Check PolicyCheck `yaml:"check" json:"check"`
	// MaxSeverity is used by PolicyCheckScan: fail if vulnerabilities meet or exceed this level.
	// Valid values: LOW, MEDIUM, HIGH, CRITICAL.
	MaxSeverity string `yaml:"max_severity,omitempty" json:"max_severity,omitempty"`
	// MaxDays is used by PolicyCheckAge: fail if image is older than this many days.
	MaxDays int `yaml:"max_days,omitempty" json:"max_days,omitempty"`
	// MaxBytes is used by PolicyCheckSize: fail if total image size exceeds this many bytes.
	MaxBytes int64 `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
}

// EffectiveConfig returns the resolved configuration for imageName by merging
// the bucket default with the first matching image override.
// More specific patterns (no wildcards, longer) take precedence over broader ones.
func (c *BucketConfig) EffectiveConfig(imageName string) ImageConfig {
	eff := c.Default

	// Sort patterns: exact matches before globs, longer before shorter.
	patterns := make([]string, 0, len(c.Images))
	for p := range c.Images {
		patterns = append(patterns, p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		iWild := strings.ContainsAny(patterns[i], "*?")
		jWild := strings.ContainsAny(patterns[j], "*?")
		if iWild != jWild {
			return !iWild // non-wildcards first
		}
		return len(patterns[i]) > len(patterns[j])
	})

	for _, pattern := range patterns {
		matched, err := path.Match(pattern, imageName)
		if err != nil || !matched {
			continue
		}
		img := c.Images[pattern]
		if img.Immutable != nil {
			eff.Immutable = img.Immutable
		}
		if img.Lifecycle != nil {
			if eff.Lifecycle == nil {
				lc := *img.Lifecycle
				eff.Lifecycle = &lc
			} else {
				merged := *eff.Lifecycle
				if img.Lifecycle.KeepLast != 0 {
					merged.KeepLast = img.Lifecycle.KeepLast
				}
				if img.Lifecycle.MaxAge != "" {
					merged.MaxAge = img.Lifecycle.MaxAge
				}
				if len(img.Lifecycle.KeepTags) > 0 {
					merged.KeepTags = img.Lifecycle.KeepTags
				}
				eff.Lifecycle = &merged
			}
		}
		break
	}
	return eff
}

// IsImmutable returns true if the effective config for imageName has immutability enabled.
func (c *BucketConfig) IsImmutable(imageName string) bool {
	eff := c.EffectiveConfig(imageName)
	return eff.Immutable != nil && *eff.Immutable
}

// GetBucketConfig reads the bucket config from storage. Returns an empty config if not set.
func GetBucketConfig(ctx context.Context, client storage.Backend, bucket string) (*BucketConfig, error) {
	data, err := client.GetObject(ctx, bucket, bucketConfigKey)
	if err != nil {
		if storage.IsNotFound(err) {
			return &BucketConfig{}, nil
		}
		return nil, fmt.Errorf("read bucket config: %w", err)
	}
	var cfg BucketConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse bucket config: %w", err)
	}
	return &cfg, nil
}

// SetBucketConfig writes the bucket config to storage.
func SetBucketConfig(ctx context.Context, client storage.Backend, bucket string, cfg *BucketConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal bucket config: %w", err)
	}
	return client.PutObject(ctx, bucket, bucketConfigKey, data)
}

// ParseConfigRef parses a config reference into bucket and optional image name.
// s3://bucket/          -> bucket="bucket",    image=""
// s3://bucket/myapp     -> bucket="bucket",    image="myapp"
// s3://bucket/dev/*     -> bucket="bucket",    image="dev/*"
// gs://bucket/myapp     -> bucket="bucket",    image="myapp"
// az://container/myapp  -> bucket="container", image="myapp"
// local://./store/      -> bucket="./store",   image=""
// local://./store/myapp -> bucket="./store",   image="myapp"
func ParseConfigRef(s3Ref string) (bucket, image string, err error) {
	var rest string
	var isLocal bool
	switch {
	case strings.HasPrefix(s3Ref, "s3://"):
		rest = strings.TrimPrefix(s3Ref, "s3://")
	case strings.HasPrefix(s3Ref, "gs://"):
		rest = strings.TrimPrefix(s3Ref, "gs://")
	case strings.HasPrefix(s3Ref, "az://"):
		rest = strings.TrimPrefix(s3Ref, "az://")
	case strings.HasPrefix(s3Ref, "local://"):
		rest = strings.TrimPrefix(s3Ref, "local://")
		isLocal = true
	default:
		return "", "", fmt.Errorf("invalid reference %q: must start with s3://, gs://, az://, or local://", s3Ref)
	}

	if isLocal && (strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, "../")) {
		firstSlash := strings.Index(rest, "/")
		after := rest[firstSlash+1:]
		secondSlash := strings.Index(after, "/")
		if secondSlash < 0 {
			return rest, "", nil
		}
		bucket = rest[:firstSlash+1+secondSlash]
		image = strings.TrimSuffix(after[secondSlash+1:], "/")
		if bucket == "" {
			return "", "", fmt.Errorf("invalid reference %q: empty bucket", s3Ref)
		}
		return bucket, image, nil
	}

	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return rest, "", nil
	}
	bucket = rest[:slashIdx]
	image = strings.TrimSuffix(rest[slashIdx+1:], "/")
	if bucket == "" {
		return "", "", fmt.Errorf("invalid reference %q: empty bucket", s3Ref)
	}
	return bucket, image, nil
}
