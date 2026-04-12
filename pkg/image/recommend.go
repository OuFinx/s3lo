package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// Recommendation describes a single actionable suggestion for the bucket.
type Recommendation struct {
	Title       string
	Description string
}

// Finding describes an observed bucket setting with a good/bad status.
type Finding struct {
	Label string
	OK    bool
}

// RecommendResult holds the findings and recommendations for a bucket.
type RecommendResult struct {
	Bucket          string
	Findings        []Finding
	Recommendations []Recommendation
}

// Recommend analyzes the actual state of a bucket and returns data-driven recommendations.
func Recommend(ctx context.Context, s3BucketRef string) (*RecommendResult, error) {
	if strings.HasPrefix(s3BucketRef, "local://") {
		return nil, fmt.Errorf("s3lo recommend is not supported for local storage")
	}

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

	result := &RecommendResult{Bucket: bucket}

	// --- Versioning ---
	vResp, err := s3c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &bucket})
	if err == nil && vResp.Status == s3types.BucketVersioningStatusEnabled {
		result.Findings = append(result.Findings, Finding{"Versioning: enabled", false})
		result.Recommendations = append(result.Recommendations, Recommendation{
			Title: "Disable bucket versioning",
			Description: "s3lo manages its own content-addressable layout — versioning adds no benefit\n" +
				"and doubles storage costs when objects are overwritten (e.g. manifest files on re-push).\n" +
				"Disable versioning and add a lifecycle rule to expire non-current versions.",
		})
	} else {
		result.Findings = append(result.Findings, Finding{"Versioning: disabled", true})
	}

	// --- Existing S3 lifecycle rules ---
	lcResp, err := s3c.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: &bucket})
	if err == nil && len(lcResp.Rules) > 0 {
		result.Findings = append(result.Findings, Finding{fmt.Sprintf("S3 lifecycle rules: %d rule(s) configured", len(lcResp.Rules)), true})
	} else {
		result.Findings = append(result.Findings, Finding{"S3 lifecycle rules: none", false})
		result.Recommendations = append(result.Recommendations, Recommendation{
			Title: "Add S3 lifecycle rule to abort incomplete multipart uploads",
			Description: "Incomplete multipart uploads accumulate storage cost without being usable.\n" +
				"Add a lifecycle rule to abort them after 7 days:\n\n" +
				"  aws s3api put-bucket-lifecycle-configuration \\\n" +
				"    --bucket " + bucket + " \\\n" +
				"    --lifecycle-configuration '{\n" +
				"      \"Rules\": [{\n" +
				"        \"ID\": \"abort-incomplete-multipart\",\n" +
				"        \"Status\": \"Enabled\",\n" +
				"        \"Filter\": {},\n" +
				"        \"AbortIncompleteMultipartUpload\": {\"DaysAfterInitiation\": 7}\n" +
				"      }]\n" +
				"    }'",
		})
	}

	// --- Incomplete multipart uploads ---
	mpu, err := s3c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: &bucket})
	if err == nil && len(mpu.Uploads) > 0 {
		result.Findings = append(result.Findings, Finding{fmt.Sprintf("Incomplete multipart uploads: %d found", len(mpu.Uploads)), false})
		result.Recommendations = append(result.Recommendations, Recommendation{
			Title: "Clean up incomplete multipart uploads now",
			Description: fmt.Sprintf("%d incomplete multipart upload(s) are consuming storage without being usable.\n", len(mpu.Uploads)) +
				"Run: aws s3api list-multipart-uploads --bucket " + bucket,
		})
	} else {
		result.Findings = append(result.Findings, Finding{"Incomplete multipart uploads: none", true})
	}

	// --- s3lo.yaml config ---
	cfg, err := GetBucketConfig(ctx, client, bucket)
	if err == nil {
		hasLifecycle := cfg.Default.Lifecycle != nil
		if !hasLifecycle {
			for _, img := range cfg.Images {
				if img.Lifecycle != nil {
					hasLifecycle = true
					break
				}
			}
		}
		if hasLifecycle {
			result.Findings = append(result.Findings, Finding{"s3lo lifecycle config: configured", true})
			result.Recommendations = append(result.Recommendations, Recommendation{
				Title: "Schedule s3lo clean to enforce lifecycle rules automatically",
				Description: "Lifecycle rules are configured in s3lo.yaml but only enforced when\n" +
					"s3lo clean is run. Schedule it to run automatically:\n\n" +
					"  # GitHub Actions — add to .github/workflows/cleanup.yml:\n" +
					"  on:\n" +
					"    schedule:\n" +
					"      - cron: '0 2 * * *'\n" +
					"  jobs:\n" +
					"    clean:\n" +
					"      runs-on: ubuntu-latest\n" +
					"      steps:\n" +
					"        - uses: actions/checkout@v4\n" +
					"        - run: s3lo clean s3://" + bucket + "/ --confirm\n\n" +
					"  # Other options:\n" +
					"  #   cron on a server:   0 2 * * * s3lo clean s3://" + bucket + "/ --confirm\n" +
					"  #   AWS Lambda + EventBridge: deploy s3lo as a Lambda, trigger nightly via EventBridge\n" +
					"  #   Terraform: aws_lambda_function + aws_cloudwatch_event_rule + aws_cloudwatch_event_target",
			})
		} else {
			result.Findings = append(result.Findings, Finding{"s3lo lifecycle config: not configured", false})
			result.Recommendations = append(result.Recommendations, Recommendation{
				Title: "Configure lifecycle rules to automatically clean old tags",
				Description: "No lifecycle rules are set. Without them, old tags and blobs accumulate indefinitely.\n" +
					"Example:\n\n" +
					"  s3lo config set s3://" + bucket + "/ lifecycle.keep_last=10 lifecycle.max_age=90d\n" +
					"  s3lo clean s3://" + bucket + "/ --confirm",
			})
		}
	}

	return result, nil
}
