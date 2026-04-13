package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// InitCheck describes a single check performed during bucket initialization.
type InitCheck struct {
	Label string `json:"label" yaml:"label"`
	OK    bool   `json:"ok" yaml:"ok"`
	Note  string `json:"note,omitempty" yaml:"note,omitempty"`
}

// InitResult holds the outcome of a bucket initialization.
type InitResult struct {
	Bucket      string      `json:"bucket" yaml:"bucket"`
	Checks      []InitCheck `json:"checks" yaml:"checks"`
	ConfigWrote bool        `json:"config_wrote" yaml:"config_wrote"`
}

// defaultBucketConfig is the s3lo.yaml written on init.
var defaultBucketConfig = `# s3lo bucket configuration
# See: https://oufinx.github.io/s3lo/commands/config/

default:
  lifecycle:
    keep_last: 10
    max_age: 90d
`

// Init verifies bucket access, checks Intelligent-Tiering, and writes a default s3lo.yaml.
// It returns an InitResult describing what was found and done.
func Init(ctx context.Context, s3BucketRef string) (*InitResult, error) {
	if strings.HasPrefix(s3BucketRef, "local://") {
		return nil, fmt.Errorf("use s3lo init --local for local storage")
	}

	bucket, _, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := storage.NewS3Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	result := &InitResult{Bucket: bucket}

	// Verify bucket exists and credentials are valid via GetBucketLocation.
	s3c, err := client.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket not accessible: %w", err)
	}
	result.Checks = append(result.Checks, InitCheck{
		Label: "Bucket exists and is accessible",
		OK:    true,
	})

	// Check Intelligent-Tiering configuration.
	itResp, err := s3c.ListBucketIntelligentTieringConfigurations(ctx,
		&s3.ListBucketIntelligentTieringConfigurationsInput{Bucket: &bucket})
	if err == nil && len(itResp.IntelligentTieringConfigurationList) > 0 {
		result.Checks = append(result.Checks, InitCheck{
			Label: "Intelligent-Tiering configuration detected",
			OK:    true,
		})
	} else {
		result.Checks = append(result.Checks, InitCheck{
			Label: "Intelligent-Tiering not configured",
			OK:    false,
			Note:  "Enable Intelligent-Tiering for automatic cost optimization on infrequently accessed blobs",
		})
	}

	// Write default s3lo.yaml if not already present.
	_, cfgErr := client.GetObject(ctx, bucket, bucketConfigKey)
	if cfgErr != nil {
		// Config doesn't exist — write default.
		if err := client.PutObject(ctx, bucket, bucketConfigKey, []byte(defaultBucketConfig)); err != nil {
			result.Checks = append(result.Checks, InitCheck{
				Label: "Write default s3lo.yaml",
				OK:    false,
				Note:  err.Error(),
			})
		} else {
			result.Checks = append(result.Checks, InitCheck{
				Label: "Created s3lo.yaml with recommended defaults",
				OK:    true,
			})
			result.ConfigWrote = true
		}
	} else {
		result.Checks = append(result.Checks, InitCheck{
			Label: "s3lo.yaml already exists — skipped",
			OK:    true,
		})
	}

	return result, nil
}
