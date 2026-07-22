package image

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// makeMultiArchStore builds a content-addressable multi-arch store whose blob keys
// are the real sha256 of their contents (so pull/copy digest verification passes),
// while the manifest "size" fields stay at fixed values (90/60/90/70) so the logical
// size assertions (310 total) remain meaningful and independent of content length.
func makeMultiArchStore(t *testing.T, parentDir, storeName, imageName, tag string) string {
	t.Helper()

	storeDir := filepath.Join(parentDir, storeName)

	writeBlob := func(content []byte) string {
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", digest), content)
		return digest
	}

	cfg1 := writeBlob([]byte("cfg1"))
	layer1 := writeBlob([]byte("layer1"))
	cfg2 := writeBlob([]byte("cfg2"))
	layer2 := writeBlob([]byte("layer2"))

	manifest1 := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":90},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:%s","size":60}]}`, cfg1, layer1))
	manifest2 := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":90},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:%s","size":70}]}`, cfg2, layer2))

	m1 := writeBlob(manifest1)
	m2 := writeBlob(manifest2)

	index := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":200,"platform":{"os":"linux","architecture":"amd64"}},{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":200,"platform":{"os":"linux","architecture":"arm64"}}]}`, m1, m2))

	writeTestFile(t, filepath.Join(storeDir, "manifests", imageName, tag, "manifest.json"), index)

	return "local://./" + storeName + "/" + imageName + ":" + tag
}

func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})
}

func TestStats_MultiArchLogicalSize(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	chdirTemp(t, parentDir)

	makeMultiArchStore(t, parentDir, "mystore", "myapp", "v1.0")
	result, err := Stats(ctx, "local://./mystore/")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	if result.LogicalBytes != 310 {
		t.Fatalf("LogicalBytes = %d, want 310", result.LogicalBytes)
	}
	if result.Tags != 1 {
		t.Fatalf("Tags = %d, want 1", result.Tags)
	}
}

func TestValidateSizePolicy_MultiArchUsesTotalLogicalSize(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	chdirTemp(t, parentDir)

	ref := makeMultiArchStore(t, parentDir, "mystore", "myapp", "v1.0")
	client := storage.NewLocalClient()
	cfg := &BucketConfig{
		Policies: []PolicyRule{{Name: "max-size", Check: PolicyCheckSize, MaxBytes: 200}},
	}
	if err := SetBucketConfig(ctx, client, filepath.Join(parentDir, "mystore"), cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Validate(ctx, ref, ValidateOptions{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.AllPassed {
		t.Fatalf("expected size policy failure, got %+v", result.Results)
	}
}

func TestCopy_MultiArchHistoryRecordsNonZeroSize(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	chdirTemp(t, parentDir)

	srcRef := makeMultiArchStore(t, parentDir, "srcstore", "myapp", "v1.0")
	destRef := "local://./dststore/myapp:v1.0"

	if _, err := Copy(ctx, srcRef, destRef, CopyOptions{}); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	historyPath := filepath.Join(parentDir, "dststore", "manifests", "myapp", "v1.0", "history.json")
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}

	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected history entries")
	}
	if entries[0].SizeBytes != 310 {
		t.Fatalf("history size = %d, want 310", entries[0].SizeBytes)
	}
}
