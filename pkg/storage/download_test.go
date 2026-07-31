package storage

import (
	"os"
	"path/filepath"
	"testing"
)

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
