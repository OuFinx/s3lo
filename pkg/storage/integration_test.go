package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Integration tests against a real S3-compatible endpoint (MinIO in CI).
// Enable by setting S3LO_TEST_ENDPOINT, S3LO_TEST_BUCKET, and AWS creds/region.
// Skipped by default so `go test ./...` stays hermetic.
func transferCtx(t *testing.T) (context.Context, string) {
	t.Helper()
	endpoint := os.Getenv("S3LO_TEST_ENDPOINT")
	bucket := os.Getenv("S3LO_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set S3LO_TEST_ENDPOINT and S3LO_TEST_BUCKET to run S3 integration tests")
	}
	return WithEndpoint(context.Background(), endpoint), bucket
}

// writePattern fills path with size bytes of a position-dependent pattern and
// returns the sha256 of the contents. The pattern depends on absolute offset, so
// parts landing out of order, overlapping, or truncated all change the digest.
func writePattern(t *testing.T, path string, size int64) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	h := sha256.New()
	w := io.MultiWriter(f, h)
	buf := make([]byte, 1<<20)
	for written := int64(0); written < size; {
		n := int64(len(buf))
		if remaining := size - written; remaining < n {
			n = remaining
		}
		for i := int64(0); i < n; i++ {
			// Derive each byte from its absolute offset in the object.
			buf[i] = byte((written + i) * 31 >> 3)
		}
		if _, err := w.Write(buf[:n]); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		written += n
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestIntegration_DirectoryRoundTrip covers UploadDirectory and DownloadDirectory,
// including nested keys. It replaces a pair of older tests that built a client
// without the context endpoint and so silently targeted real AWS.
func TestIntegration_DirectoryRoundTrip(t *testing.T) {
	ctx, bucket := transferCtx(t)

	client, err := NewBackendFromRef(ctx, fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	want := map[string]string{
		"manifest.json":         `{"test":true}`,
		"config.json":           `{"arch":"amd64"}`,
		"blobs/sha256/deadbeef": "layer one",
		"blobs/sha256/cafebabe": "layer two",
	}

	srcDir := t.TempDir()
	for rel, content := range want {
		path := filepath.Join(srcDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// UploadDirectory is only on the S3 *Client, not the Backend interface.
	s3Client, ok := client.(*Client)
	if !ok {
		t.Fatalf("expected *Client for an s3:// ref, got %T", client)
	}

	prefix := "s3lo-test/dir-roundtrip"
	if err := s3Client.UploadDirectory(ctx, srcDir, bucket, prefix); err != nil {
		t.Fatalf("upload directory: %v", err)
	}
	t.Cleanup(func() {
		keys, err := client.ListKeys(context.WithoutCancel(ctx), bucket, prefix)
		if err != nil {
			t.Logf("cleanup list %s: %v", prefix, err)
			return
		}
		if err := client.DeleteObjects(context.WithoutCancel(ctx), bucket, keys); err != nil {
			t.Logf("cleanup delete %s: %v", prefix, err)
		}
	})

	dstDir := t.TempDir()
	if err := client.DownloadDirectory(ctx, bucket, prefix, dstDir); err != nil {
		t.Fatalf("download directory: %v", err)
	}

	for rel, content := range want {
		got, err := os.ReadFile(filepath.Join(dstDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read back %s: %v", rel, err)
		}
		if string(got) != content {
			t.Errorf("%s: got %q, want %q", rel, got, content)
		}
	}
}

// TestIntegration_MultipartRoundTrip covers objects that span more than one
// transfer part, which is the only path that exercises parallel multipart upload
// and parallel ranged download. A single-part object would pass even if the
// concurrent assembly were broken.
func TestIntegration_MultipartRoundTrip(t *testing.T) {
	ctx, bucket := transferCtx(t)

	// NewBackendFromRef (not NewS3Client) is what applies the endpoint carried on
	// the context, so this is the path that reaches MinIO.
	client, err := NewBackendFromRef(ctx, fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	// Two full parts plus a remainder, so the final short part is covered too.
	size := int64(transferPartSize)*2 + 1234
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	want := writePattern(t, src, size)

	key := fmt.Sprintf("s3lo-test/multipart-%s.bin", want[:16])
	if err := client.UploadFile(ctx, src, bucket, key, StorageClassStandard); err != nil {
		t.Fatalf("upload: %v", err)
	}
	t.Cleanup(func() {
		if err := client.DeleteObjects(context.WithoutCancel(ctx), bucket, []string{key}); err != nil {
			t.Logf("cleanup %s: %v", key, err)
		}
	})

	dst := filepath.Join(dir, "dst.bin")
	if err := client.DownloadObjectToFile(ctx, bucket, key, dst); err != nil {
		t.Fatalf("download: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat %s: %v", dst, err)
	}
	if info.Size() != size {
		t.Fatalf("size mismatch: got %d, want %d", info.Size(), size)
	}
	if got := sha256File(t, dst); got != want {
		t.Fatalf("content mismatch after multipart round trip: got %s, want %s", got, want)
	}
}
