package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadDirectory_Integration(t *testing.T) {
	if os.Getenv("S3LO_TEST_BUCKET") == "" {
		t.Skip("set S3LO_TEST_BUCKET to run S3 integration tests")
	}
	bucket := os.Getenv("S3LO_TEST_BUCKET")

	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "blobs"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "manifest.json"), []byte(`{"test":true}`), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"arch":"amd64"}`), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "blobs", "sha256_abc"), []byte("fake layer data"), 0o644)

	c, err := NewS3Client(context.Background())
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}

	err = c.UploadDirectory(context.Background(), tmpDir, bucket, "test-image/v0.0.1")
	if err != nil {
		t.Fatalf("UploadDirectory: %v", err)
	}
}

func TestBuildS3Key_NestedPath(t *testing.T) {
	key := buildS3Key("myapp/v1.0", "/tmp/oci", "/tmp/oci/blobs/sha256/abc123")
	want := "myapp/v1.0/blobs/sha256/abc123"
	if key != want {
		t.Errorf("buildS3Key() = %q, want %q", key, want)
	}
}

func TestBuildS3Key(t *testing.T) {
	localPath := "/tmp/oci/blobs/sha256_abc"
	baseDir := "/tmp/oci"
	prefix := "myapp/v1.0"

	key := buildS3Key(prefix, baseDir, localPath)
	want := "myapp/v1.0/blobs/sha256_abc"
	if key != want {
		t.Errorf("buildS3Key() = %q, want %q", key, want)
	}
}
