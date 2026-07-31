package storage

import "testing"

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
