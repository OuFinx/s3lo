package image

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// ValidateOptions controls policy validation behavior.
type ValidateOptions struct {
	// TrivyPath is the path to the trivy binary (required for scan checks).
	TrivyPath string
}

// PolicyResult holds the result of a single policy check.
type PolicyResult struct {
	Name    string `json:"name"`
	Check   string `json:"check"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// ValidateResult holds the aggregate result of all policy checks.
type ValidateResult struct {
	Reference string         `json:"reference"`
	Results   []PolicyResult `json:"results"`
	AllPassed bool           `json:"all_passed"`
}

// Validate runs all policies in the bucket's s3lo.yaml against the given image tag.
// Returns AllPassed=true only when every policy passes.
// Scan checks require opts.TrivyPath to be set.
func Validate(ctx context.Context, s3Ref string, opts ValidateOptions) (*ValidateResult, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	cfg, err := GetBucketConfig(ctx, client, parsed.Bucket)
	if err != nil {
		return nil, fmt.Errorf("load bucket config: %w", err)
	}

	result := &ValidateResult{Reference: s3Ref, AllPassed: true}
	if len(cfg.Policies) == 0 {
		return result, nil
	}

	for _, policy := range cfg.Policies {
		pr, err := runPolicy(ctx, client, parsed, policy, opts)
		if err != nil {
			return nil, fmt.Errorf("policy %q: %w", policy.Name, err)
		}
		result.Results = append(result.Results, pr)
		if !pr.Passed {
			result.AllPassed = false
		}
	}

	return result, nil
}

func runPolicy(ctx context.Context, client storage.Backend, parsed ref.Reference, policy PolicyRule, opts ValidateOptions) (PolicyResult, error) {
	switch policy.Check {
	case PolicyCheckScan:
		return runScanPolicy(ctx, parsed, policy, opts.TrivyPath)
	case PolicyCheckAge:
		return runAgePolicy(ctx, client, parsed, policy)
	case PolicyCheckSigned:
		return runSignedPolicy(ctx, client, parsed, policy)
	case PolicyCheckSize:
		return runSizePolicy(ctx, parsed, policy)
	default:
		return PolicyResult{}, fmt.Errorf("unknown check type %q", policy.Check)
	}
}

func runScanPolicy(ctx context.Context, parsed ref.Reference, policy PolicyRule, trivyPath string) (PolicyResult, error) {
	pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
	if trivyPath == "" {
		return pr, fmt.Errorf("TrivyPath required for scan policy")
	}
	exitCode, err := Scan(ctx, parsed.String(), ScanOptions{
		TrivyPath: trivyPath,
		Severity:  policy.MaxSeverity,
		Format:    "table",
	})
	if err != nil {
		return pr, err
	}
	if exitCode != 0 {
		pr.Message = fmt.Sprintf("vulnerabilities found at or above %s severity", policy.MaxSeverity)
	} else {
		pr.Passed = true
	}
	return pr, nil
}

func runAgePolicy(ctx context.Context, client storage.Backend, parsed ref.Reference, policy PolicyRule) (PolicyResult, error) {
	pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
	histKey := parsed.ManifestsPrefix() + "history.json"
	data, err := client.GetObject(ctx, parsed.Bucket, histKey)
	if err != nil {
		if storage.IsNotFound(err) {
			pr.Message = "no push history found"
			return pr, nil
		}
		return pr, err
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return pr, fmt.Errorf("parse history: %w", err)
	}
	if len(entries) == 0 {
		pr.Message = "no push history found"
		return pr, nil
	}
	latest := entries[0].PushedAt
	ageDays := int(time.Since(latest).Hours() / 24)
	if ageDays > policy.MaxDays {
		pr.Message = fmt.Sprintf("image is %d days old, limit is %d", ageDays, policy.MaxDays)
	} else {
		pr.Passed = true
	}
	return pr, nil
}

func runSignedPolicy(ctx context.Context, client storage.Backend, parsed ref.Reference, policy PolicyRule) (PolicyResult, error) {
	pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
	if policy.KeyRef == "" {
		pr.Message = "signed policy requires key_ref"
		return pr, nil
	}
	result, err := Verify(ctx, parsed.String(), policy.KeyRef)
	if err != nil {
		return pr, err
	}
	if result.Verified {
		pr.Passed = true
		pr.Message = fmt.Sprintf("verified by %s", result.KeyID)
	} else {
		pr.Message = result.Reason
	}
	return pr, nil
}

func runSizePolicy(ctx context.Context, parsed ref.Reference, policy PolicyRule) (PolicyResult, error) {
	pr := PolicyResult{Name: policy.Name, Check: string(policy.Check)}
	client, err := storage.NewBackendFromRef(ctx, parsed.String())
	if err != nil {
		return pr, err
	}
	manifestData, err := client.GetObject(ctx, parsed.Bucket, parsed.ManifestsPrefix()+"manifest.json")
	if err != nil {
		return pr, err
	}
	totalSize := manifestLogicalSize(ctx, client, parsed.Bucket, manifestData)
	if totalSize > policy.MaxBytes {
		pr.Message = fmt.Sprintf("image size %d bytes exceeds limit %d bytes", totalSize, policy.MaxBytes)
	} else {
		pr.Passed = true
	}
	return pr, nil
}
