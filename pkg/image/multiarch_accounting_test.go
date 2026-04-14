package image

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

func makeMultiArchStore(t *testing.T, parentDir, storeName, imageName, tag string) string {
	t.Helper()

	storeDir := filepath.Join(parentDir, storeName)
	index := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":200,"platform":{"os":"linux","architecture":"amd64"}},{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","size":200,"platform":{"os":"linux","architecture":"arm64"}}]}`)
	manifest1 := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:3333333333333333333333333333333333333333333333333333333333333333","size":90},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:4444444444444444444444444444444444444444444444444444444444444444","size":60}]}`)
	manifest2 := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:5555555555555555555555555555555555555555555555555555555555555555","size":90},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:6666666666666666666666666666666666666666666666666666666666666666","size":70}]}`)

	writeTestFile(t, filepath.Join(storeDir, "manifests", imageName, tag, "manifest.json"), index)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("1", 64)), manifest1)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("2", 64)), manifest2)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("3", 64)), []byte("cfg1"))
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("4", 64)), []byte("layer1"))
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("5", 64)), []byte("cfg2"))
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("6", 64)), []byte("layer2"))

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
