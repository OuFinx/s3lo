package image

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyBytesDigest(t *testing.T) {
	data := []byte("hello s3lo")
	want := fmt.Sprintf("%x", sha256.Sum256(data))

	if err := verifyBytesDigest(data, want); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	if err := verifyBytesDigest(data, "deadbeef"); err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	// A single flipped byte must be rejected (tamper detection).
	if err := verifyBytesDigest([]byte("hello s3lp"), want); err == nil {
		t.Fatal("expected mismatch for tampered content, got nil")
	}
}

func TestVerifyFileDigest(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "blob")
	data := []byte("layer bytes")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(data))

	if err := verifyFileDigest(p, want); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	if err := verifyFileDigest(p, "deadbeef"); err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	if err := verifyFileDigest(filepath.Join(dir, "missing"), want); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
