package storage

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLocalClient_PathTraversal(t *testing.T) {
	bucket := t.TempDir()
	c := NewLocalClient()
	ctx := context.Background()

	traversalKeys := []string{
		"../../etc/passwd",
		"../outside",
		"blobs/../../../etc/shadow",
	}

	for _, key := range traversalKeys {
		if _, err := c.GetObject(ctx, bucket, key); err == nil || !strings.Contains(err.Error(), "escapes bucket root") {
			t.Errorf("GetObject(%q): expected escapes-bucket-root error, got %v", key, err)
		}
		if err := c.PutObject(ctx, bucket, key, []byte("x")); err == nil || !strings.Contains(err.Error(), "escapes bucket root") {
			t.Errorf("PutObject(%q): expected escapes-bucket-root error, got %v", key, err)
		}
		if err := c.DeleteObjects(ctx, bucket, []string{key}); err == nil || !strings.Contains(err.Error(), "escapes bucket root") {
			t.Errorf("DeleteObjects(%q): expected escapes-bucket-root error, got %v", key, err)
		}
	}
}

func TestLocalClient_NormalKey(t *testing.T) {
	bucket := t.TempDir()
	c := NewLocalClient()
	ctx := context.Background()

	if err := c.PutObject(ctx, bucket, "blobs/sha256/abc", []byte("data")); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	data, err := c.GetObject(ctx, bucket, "blobs/sha256/abc")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(data) != "data" {
		t.Fatalf("unexpected data: %q", data)
	}
	if err := os.Remove(bucket + "/blobs/sha256/abc"); err != nil {
		t.Fatal(err)
	}
}
