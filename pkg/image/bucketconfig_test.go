package image

import (
	"context"
	"errors"
	"github.com/OuFinx/s3lo/pkg/chunk"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	yaml2 "gopkg.in/yaml.v3"
	"testing"
)

func TestBucketConfigPoliciesRoundtrip(t *testing.T) {
	input := `
policies:
  - name: no-critical-vulns
    check: scan
    max_severity: HIGH
  - name: max-age
    check: age
    max_days: 90
  - name: require-signature
    check: signed
    key_ref: cosign.pub
  - name: max-size
    check: size
    max_bytes: 1073741824
`
	var cfg BucketConfig
	if err := yaml2.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Policies) != 4 {
		t.Fatalf("expected 4 policies, got %d", len(cfg.Policies))
	}
	if cfg.Policies[0].Name != "no-critical-vulns" {
		t.Errorf("unexpected name: %s", cfg.Policies[0].Name)
	}
	if cfg.Policies[0].Check != PolicyCheckScan {
		t.Errorf("unexpected check: %s", cfg.Policies[0].Check)
	}
	if cfg.Policies[0].MaxSeverity != "HIGH" {
		t.Errorf("unexpected max_severity: %s", cfg.Policies[0].MaxSeverity)
	}
	if cfg.Policies[1].MaxDays != 90 {
		t.Errorf("unexpected max_days: %d", cfg.Policies[1].MaxDays)
	}
	if cfg.Policies[2].KeyRef != "cosign.pub" {
		t.Errorf("unexpected key_ref: %s", cfg.Policies[2].KeyRef)
	}
	if cfg.Policies[3].MaxBytes != 1073741824 {
		t.Errorf("unexpected max_bytes: %d", cfg.Policies[3].MaxBytes)
	}
}

// TestEnsureChunkFormat_RejectsMismatch guards the failure that would be almost
// invisible: chunks written under different parameters never match, so a bucket
// would keep a full second copy of everything while reporting 0% deduplication
// and no error at all.
func TestEnsureChunkFormat_RejectsMismatch(t *testing.T) {
	ctx := context.Background()
	client := storage.NewLocalClient()
	bucket := t.TempDir()

	// First chunked push stamps the bucket.
	cfg := &BucketConfig{}
	if err := ensureChunkFormat(ctx, client, bucket, cfg); err != nil {
		t.Fatalf("stamping a fresh bucket: %v", err)
	}
	if cfg.ChunkFormat != chunk.FormatVersion {
		t.Fatalf("ChunkFormat = %d, want %d", cfg.ChunkFormat, chunk.FormatVersion)
	}

	// The stamp must persist, not just live in memory.
	stored, err := GetBucketConfig(ctx, client, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ChunkFormat != chunk.FormatVersion {
		t.Fatalf("stored ChunkFormat = %d, want %d", stored.ChunkFormat, chunk.FormatVersion)
	}

	// Same version is a no-op.
	if err := ensureChunkFormat(ctx, client, bucket, stored); err != nil {
		t.Fatalf("matching version rejected: %v", err)
	}

	// A bucket from an incompatible build must be refused, not written into.
	old := &BucketConfig{ChunkFormat: chunk.FormatVersion + 1}
	err = ensureChunkFormat(ctx, client, bucket, old)
	if !errors.Is(err, ErrChunkFormatMismatch) {
		t.Fatalf("mismatched format returned %v, want ErrChunkFormatMismatch", err)
	}
}
