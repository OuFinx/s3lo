package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// Recommendation describes a single lifecycle rule recommendation.
type Recommendation struct {
	Title       string
	Description string
}

// RecommendResult holds generated lifecycle rule recommendations for a bucket.
type RecommendResult struct {
	Bucket          string
	VersioningOn    bool
	Recommendations []Recommendation
	TerraformHCL    string
}

// Recommend analyzes a bucket and generates S3 Lifecycle Rule recommendations.
// The recommendations cover blob tiering, multipart upload cleanup, and versioning.
func Recommend(ctx context.Context, s3BucketRef string) (*RecommendResult, error) {
	bucket, _, err := ParseBucketRef(s3BucketRef)
	if err != nil {
		return nil, err
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	s3c, err := client.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("get S3 client for bucket: %w", err)
	}

	// Check bucket versioning status.
	versioningOn := false
	vResp, err := s3c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &bucket})
	if err == nil && vResp.Status == s3types.BucketVersioningStatusEnabled {
		versioningOn = true
	}

	result := &RecommendResult{
		Bucket:       bucket,
		VersioningOn: versioningOn,
	}

	result.Recommendations = []Recommendation{
		{
			Title: "Move blobs to Infrequent Access after 30 days",
			Description: "Prefix: blobs/sha256/\n" +
				"Transition: STANDARD -> STANDARD_IA after 30 days\n" +
				"Blobs (image layers) are large and rarely accessed after initial pull.\n" +
				"STANDARD_IA costs ~40% less with no performance difference for pull.",
		},
		{
			Title:       "Expire incomplete multipart uploads after 7 days",
			Description: "Incomplete multipart uploads accumulate storage cost without being usable.\n" + "Cleaning them up after 7 days prevents orphaned storage charges.",
		},
	}

	if versioningOn {
		result.Recommendations = append(result.Recommendations, Recommendation{
			Title: "Delete non-current object versions after 14 days",
			Description: "Bucket versioning is enabled. Old versions of manifests and blobs\n" +
				"accumulate over time. 14 days gives enough time for recovery if needed.",
		})
	}

	result.TerraformHCL = buildTerraformHCL(bucket, versioningOn)
	return result, nil
}

func buildTerraformHCL(bucket string, versioning bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`resource "aws_s3_bucket_lifecycle_configuration" "s3lo" {
  bucket = %q

  rule {
    id     = "s3lo-blob-tiering"
    status = "Enabled"
    filter { prefix = "blobs/" }
    transition {
      days          = 30
      storage_class = "STANDARD_IA"
    }
  }

  rule {
    id     = "s3lo-abort-multipart"
    status = "Enabled"
    filter {}
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
`, bucket))

	if versioning {
		b.WriteString(`
  rule {
    id     = "s3lo-noncurrent-expiry"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration {
      noncurrent_days = 14
    }
  }
`)
	}

	b.WriteString("}\n")
	return b.String()
}
