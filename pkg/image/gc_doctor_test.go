package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupMultiArchStore(t *testing.T) string {
	t.Helper()

	parentDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parentDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})

	storeDir := filepath.Join(parentDir, "mystore")

	index := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":123,"platform":{"os":"linux","architecture":"amd64"}}]}`)
	platformManifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","size":10},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:3333333333333333333333333333333333333333333333333333333333333333","size":20}]}`)

	writeTestFile(t, filepath.Join(storeDir, "manifests", "myapp", "latest", "manifest.json"), index)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("1", 64)), platformManifest)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("2", 64)), []byte("config"))
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("3", 64)), []byte("layer"))
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("4", 64)), []byte("orphan"))

	old := time.Now().Add(-2 * time.Hour)
	orphanPath := filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("4", 64))
	if err := os.Chtimes(orphanPath, old, old); err != nil {
		t.Fatal(err)
	}

	return "local://./mystore/"
}

func TestGC_MultiArchKeepsReferencedBlobs(t *testing.T) {
	ctx := context.Background()
	ref := setupMultiArchStore(t)

	result, err := GC(ctx, ref, true)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1 orphan only", result.Deleted)
	}
	if result.Scanned != 4 {
		t.Fatalf("Scanned = %d, want 4 blobs", result.Scanned)
	}
}

func TestDoctor_MultiArchDoesNotReportReferencedBlobsAsOrphans(t *testing.T) {
	ctx := context.Background()
	ref := setupMultiArchStore(t)

	result, err := Doctor(ctx, ref)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	if len(result.ManifestIssues) != 0 {
		t.Fatalf("ManifestIssues = %+v, want none", result.ManifestIssues)
	}
	if result.OrphanedBlobs != 1 {
		t.Fatalf("OrphanedBlobs = %d, want 1 real orphan", result.OrphanedBlobs)
	}
}

func TestDoctor_MultiArchReportsMissingPlatformManifestBlob(t *testing.T) {
	ctx := context.Background()
	ref := setupMultiArchStore(t)

	missing := filepath.Join(".", "mystore", "blobs", "sha256", strings.Repeat("1", 64))
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	result, err := Doctor(ctx, ref)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	if len(result.ManifestIssues) != 1 {
		t.Fatalf("ManifestIssues = %+v, want 1 issue", result.ManifestIssues)
	}
	if !strings.Contains(result.ManifestIssues[0].Message, "missing blob(s)") {
		t.Fatalf("unexpected issue message: %s", result.ManifestIssues[0].Message)
	}
}
