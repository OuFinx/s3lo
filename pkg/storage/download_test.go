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

	localPath, err := buildLocalPath(destDir, prefix, key)
	if err != nil {
		t.Fatalf("buildLocalPath() error: %v", err)
	}
	want := "/tmp/download/blobs/sha256_abc"
	if localPath != want {
		t.Errorf("buildLocalPath() = %q, want %q", localPath, want)
	}
}

func TestSafeJoinUnder(t *testing.T) {
	root := filepath.Join(os.TempDir(), "s3lo-root")
	ok := []struct{ rel, wantSuffix string }{
		{"a/b.txt", filepath.Join("s3lo-root", "a", "b.txt")},
		{"blobs/sha256/deadbeef", filepath.Join("s3lo-root", "blobs", "sha256", "deadbeef")},
	}
	for _, tc := range ok {
		got, err := safeJoinUnder(root, tc.rel)
		if err != nil {
			t.Fatalf("safeJoinUnder(%q) unexpected error: %v", tc.rel, err)
		}
		if !filepath.IsAbs(got) || filepath.Base(filepath.Dir(got)) == ".." {
			t.Fatalf("safeJoinUnder(%q) = %q looks wrong", tc.rel, got)
		}
	}
	bad := []string{"../evil", "../../etc/passwd", "a/../../../etc/passwd"}
	for _, rel := range bad {
		if _, err := safeJoinUnder(root, rel); err == nil {
			t.Fatalf("safeJoinUnder(%q) should have been rejected as traversal", rel)
		}
	}
}

func TestBuildLocalPath_RejectsTraversal(t *testing.T) {
	root := filepath.Join(os.TempDir(), "s3lo-dl")
	if _, err := buildLocalPath(root, "prefix/", "prefix/../../../etc/passwd"); err == nil {
		t.Fatal("buildLocalPath should reject a traversal key")
	}
	if _, err := buildLocalPath(root, "prefix/", "prefix/blobs/sha256/abc"); err != nil {
		t.Fatalf("buildLocalPath rejected a valid key: %v", err)
	}
}
