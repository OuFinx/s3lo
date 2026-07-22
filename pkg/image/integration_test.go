package image

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// Integration tests against a real S3-compatible endpoint (MinIO in CI).
// Enable by setting S3LO_TEST_ENDPOINT, S3LO_TEST_BUCKET, and AWS creds/region.
// They are skipped by default so `go test ./...` stays hermetic.
func integrationCtx(t *testing.T) (context.Context, string) {
	t.Helper()
	endpoint := os.Getenv("S3LO_TEST_ENDPOINT")
	bucket := os.Getenv("S3LO_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set S3LO_TEST_ENDPOINT and S3LO_TEST_BUCKET to run S3 integration tests")
	}
	return storage.WithEndpoint(context.Background(), endpoint), bucket
}

// putS3Manifest writes a minimal single-arch image (config + one layer + manifest)
// to the S3 backend using real content-addressable digests, returning the tag ref.
func putS3Manifest(t *testing.T, ctx context.Context, client storage.Backend, bucket, image, tag string) string {
	t.Helper()
	put := func(content []byte) string {
		d := fmt.Sprintf("%x", sha256.Sum256(content))
		if err := client.PutObject(ctx, bucket, "blobs/sha256/"+d, content); err != nil {
			t.Fatalf("put blob: %v", err)
		}
		return d
	}
	cfg := put([]byte(`{"architecture":"amd64","os":"linux"}`))
	layer := put([]byte("integration-layer-bytes"))
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":37},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:%s","size":23}]}`, cfg, layer))
	key := "manifests/" + image + "/" + tag + "/manifest.json"
	if err := client.PutObject(ctx, bucket, key, manifest); err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	if err := client.PutObject(ctx, bucket, "manifests/"+image+"/"+tag+"/oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)); err != nil {
		t.Fatalf("put oci-layout: %v", err)
	}
	return fmt.Sprintf("s3://%s/%s:%s", bucket, image, tag)
}

// TestIntegration_ImageRoundTrip builds an OCI store directly in S3, then exercises
// list/stats/inspect/dedup(copy)/delete/GC against the real backend.
func TestIntegration_ImageRoundTrip(t *testing.T) {
	ctx, bucket := integrationCtx(t)
	client, err := storage.NewBackendFromRef(ctx, fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		t.Fatalf("backend: %v", err)
	}

	dst := putS3Manifest(t, ctx, client, bucket, "integ-app", "v1.0")
	dst2 := fmt.Sprintf("s3://%s/integ-app:v1.1", bucket)

	// Copy within S3 to a second tag — identical content must dedup (blobs skipped).
	res2, err := Copy(ctx, dst, dst2, CopyOptions{})
	if err != nil {
		t.Fatalf("copy second tag: %v", err)
	}
	if res2.BlobsCopied != 0 {
		t.Errorf("expected all blobs deduped on second copy, got %d copied", res2.BlobsCopied)
	}

	// List.
	entries, err := List(ctx, fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	tags := 0
	for _, e := range entries {
		if e.Name == "integ-app" {
			tags = len(e.Tags)
		}
	}
	if tags < 2 {
		t.Errorf("expected 2 integ-app tags in list, found %d", tags)
	}

	// Stats — dedup savings must be non-zero (two identical tags).
	stats, err := Stats(ctx, fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.DedupSavings() <= 0 {
		t.Errorf("expected positive dedup savings, got %d", stats.DedupSavings())
	}

	// Inspect.
	if _, err := Inspect(ctx, dst); err != nil {
		t.Errorf("inspect: %v", err)
	}

	// Delete one tag; GC must run cleanly (grace period may keep fresh blobs).
	if err := Delete(ctx, dst2, false); err != nil {
		t.Errorf("delete: %v", err)
	}
	if _, err := GC(ctx, fmt.Sprintf("s3://%s/", bucket), true); err != nil {
		t.Errorf("gc dry-run: %v", err)
	}

	// Cleanup.
	_ = Delete(ctx, dst, false)
	keys, _ := client.ListKeys(ctx, bucket, "")
	if len(keys) > 0 {
		_ = client.DeleteObjects(ctx, bucket, keys)
	}
}

// TestIntegration_StorageOps exercises the storage.Backend paths that were fixed:
// CopyObject key encoding, TouchObject, and presign.
func TestIntegration_StorageOps(t *testing.T) {
	ctx, bucket := integrationCtx(t)
	client, err := storage.NewBackendFromRef(ctx, fmt.Sprintf("s3://%s/", bucket))
	if err != nil {
		t.Fatalf("backend: %v", err)
	}

	key := "integ/round trip+key.txt" // space and '+' exercise CopySource encoding
	body := []byte("hello minio integration")
	if err := client.PutObject(ctx, bucket, key, body); err != nil {
		t.Fatalf("put: %v", err)
	}
	t.Cleanup(func() { _ = client.DeleteObjects(ctx, bucket, []string{key, key + ".copy"}) })

	got, err := client.GetObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(got)) != fmt.Sprintf("%x", sha256.Sum256(body)) {
		t.Fatal("round-trip content mismatch")
	}

	// CopyObject with a key that needs URL-encoding must succeed.
	if err := client.CopyObject(ctx, bucket, key, key+".copy"); err != nil {
		t.Fatalf("copy with special chars: %v", err)
	}

	// TouchObject must not error and should not corrupt the object.
	if err := client.TouchObject(ctx, bucket, key); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if after, err := client.GetObject(ctx, bucket, key); err != nil || string(after) != string(body) {
		t.Fatalf("object changed after touch: err=%v", err)
	}

	// Presign should yield a working, self-authenticating URL.
	type presigner interface {
		PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	}
	if p, ok := client.(presigner); ok {
		url, err := p.PresignGetObject(ctx, bucket, key, time.Minute)
		if err != nil {
			t.Fatalf("presign: %v", err)
		}
		resp, err := http.Get(url) //nolint:gosec // test-controlled URL
		if err != nil {
			t.Fatalf("GET presigned: %v", err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(data) != string(body) {
			t.Fatalf("presigned GET: status=%d body=%q", resp.StatusCode, data)
		}
	}
}
