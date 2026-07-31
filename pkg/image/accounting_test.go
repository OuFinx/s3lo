package image

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestCopy_HeadObjectExistsErrorIsPropagated(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	chdirTemp(t, parentDir)

	srcRef := makeSingleArchStore(t, parentDir, "srcstore", "myapp", "v1.0")
	destRef := "local://./dststore/myapp:v1.0"

	blobsDir := filepath.Join(parentDir, "dststore", "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobsDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blobsDir, 0o755)
	})

	_, err := Copy(ctx, srcRef, destRef, CopyOptions{})
	if err == nil {
		t.Fatal("expected copy error, got nil")
	}
	if !strings.Contains(err.Error(), "check destination blob") {
		t.Fatalf("unexpected error: %v", err)
	}
}
