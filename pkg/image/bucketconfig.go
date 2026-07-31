package image

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/OuFinx/s3lo/v2/pkg/chunk"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
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
	Default ImageConfig            `yaml:"default,omitempty" json:"default,omitempty"`
	Images  map[string]ImageConfig `yaml:"images,omitempty" json:"images,omitempty"`

	// Chunked makes push store layers as content-defined chunks shared across
	// every image in the bucket, instead of one object per layer. Unset means on:
	// it is the behaviour every measured claim about s3lo describes, and a default
	// that has to be discovered in the documentation is a default that most users
	// never get. Set it to false to store whole layers.
	//
	// It is deliberately bucket-wide rather than per-image: the chunk store is
	// shared, so a per-image switch would only make reasoning about garbage
	// collection harder without making anything more useful. Turning it on or
	// off is safe at any time — reads resolve a layer through its recipe when one
	// exists and fall back to a whole-layer blob when it does not, so a bucket
	// may hold both without migration.
	//
	// The one thing it is not safe against is an s3lo older than v2.0.0, which
	// has no notion of a recipe and cannot read a chunked layer at all.
	Chunked *bool `yaml:"chunked,omitempty" json:"chunked,omitempty"`

	// ChunkFormat records the chunking parameters this bucket's chunks were
	// written with. It is stamped on the first chunked push and never changes
	// afterwards, so a build of s3lo whose parameters differ is rejected instead
	// of quietly filling the bucket with chunks that cannot match the existing
	// ones. Zero means no chunked push has happened yet.
	ChunkFormat int `yaml:"chunk_format,omitempty" json:"chunk_format,omitempty"`
}

// ChunkedEnabled reports whether push should store layers as chunks. Anything
// that has not explicitly opted out gets chunking, including a bucket with no
// config object at all.
func (c *BucketConfig) ChunkedEnabled() bool {
	return c == nil || c.Chunked == nil || *c.Chunked
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

// ErrChunkFormatMismatch is returned when a bucket's chunks were written with
// chunking parameters this build does not produce.
var ErrChunkFormatMismatch = errors.New("bucket chunk format mismatch")

// ensureChunkFormat stamps the bucket with the chunker version on first use and
// refuses to write into a bucket built by an incompatible one.
//
// Silently proceeding would be the worst outcome: every push would store a full
// second copy of content that looks identical, deduplication would read as
// broken, and nothing would say why.
func ensureChunkFormat(ctx context.Context, client storage.Backend, bucket string, cfg *BucketConfig) error {
	switch cfg.ChunkFormat {
	case chunk.FormatVersion:
		return nil
	case 0:
		cfg.ChunkFormat = chunk.FormatVersion
		if err := SetBucketConfig(ctx, client, bucket, cfg); err != nil {
			return fmt.Errorf("record chunk format: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: bucket was chunked with format %d, this build writes format %d; "+
			"chunks from the two do not deduplicate against each other",
			ErrChunkFormatMismatch, cfg.ChunkFormat, chunk.FormatVersion)
	}
}
