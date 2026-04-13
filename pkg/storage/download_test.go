package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadDirectory_Integration(t *testing.T) {
	if os.Getenv("S3LO_TEST_BUCKET") == "" {
		t.Skip("set S3LO_TEST_BUCKET to run S3 integration tests")
	}
	bucket := os.Getenv("S3LO_TEST_BUCKET")

	c, err := NewS3Client(context.Background())
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}

	tmpDir := t.TempDir()
	err = c.DownloadDirectory(context.Background(), bucket, "test-image/v0.0.1", tmpDir)
	if err != nil {
		t.Fatalf("DownloadDirectory: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "manifest.json")); os.IsNotExist(err) {
		t.Error("manifest.json not downloaded")
	}
}

func TestBuildLocalPath(t *testing.T) {
	key := "myapp/v1.0/blobs/sha256_abc"
	prefix := "myapp/v1.0"
	destDir := "/tmp/download"

	localPath := buildLocalPath(destDir, prefix, key)
	want := "/tmp/download/blobs/sha256_abc"
	if localPath != want {
		t.Errorf("buildLocalPath() = %q, want %q", localPath, want)
	}
}
