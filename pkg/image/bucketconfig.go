package image

import (
	"context"
	"errors"
	"fmt"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"gopkg.in/yaml.v3"
)

const bucketConfigKey = "s3lo.yaml"

// BucketConfig holds per-bucket s3lo configuration stored at s3://bucket/s3lo.yaml.
type BucketConfig struct {
	Immutable bool `yaml:"immutable"`
}

// GetBucketConfig reads the bucket config from S3. Returns empty config if not set.
func GetBucketConfig(ctx context.Context, client *s3client.Client, bucket string) (*BucketConfig, error) {
	data, err := client.GetObject(ctx, bucket, bucketConfigKey)
	if err != nil {
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &noSuchKey) {
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

// SetBucketConfig writes the bucket config to S3.
func SetBucketConfig(ctx context.Context, client *s3client.Client, bucket string, cfg *BucketConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal bucket config: %w", err)
	}
	return client.PutObject(ctx, bucket, bucketConfigKey, data)
}
