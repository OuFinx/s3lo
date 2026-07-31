package image

import (
	"context"
	"errors"
	"github.com/OuFinx/s3lo/v3/pkg/chunk"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
	"testing"
)

// TestChunkedEnabled_DefaultsOn pins the default. Every measured claim about
// s3lo — the 4 MB re-push, the deduplication, the smaller bucket — describes a
// chunked bucket, so a user who never touches the config must get one.
func TestChunkedEnabled_DefaultsOn(t *testing.T) {
	off, on := false, true
	cases := []struct {
		name string
		cfg  *BucketConfig
		want bool
	}{
		{"no config object at all", nil, true},
		{"config present, key unset", &BucketConfig{}, true},
		{"explicitly disabled", &BucketConfig{Chunked: &off}, false},
		{"explicitly enabled", &BucketConfig{Chunked: &on}, true},
	}
	for _, tc := range cases {
		if got := tc.cfg.ChunkedEnabled(); got != tc.want {
			t.Errorf("%s: ChunkedEnabled() = %v, want %v", tc.name, got, tc.want)
		}
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
