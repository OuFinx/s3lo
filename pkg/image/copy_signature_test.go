package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeBlob stores data under blobs/sha256/<digest> and returns the digest.
func writeBlob(t *testing.T, bucketDir string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	d := hex.EncodeToString(sum[:])
	dir := filepath.Join(bucketDir, "blobs", "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, d), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

// setupSignedMultiArchStore writes a two-platform image index at myapp:v1.0 in a local
// store, and returns the store dir plus a cosign key pair.
func setupSignedMultiArchStore(t *testing.T) (bucketRelDir, keyFile, pubFile string) {
	t.Helper()
	_, bucketRelDir, keyFile, pubFile = setupSignedStore(t)

	amd := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	arm := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[],"annotations":{"arch":"arm64"}}`)
	amdDigest := writeBlob(t, bucketRelDir, amd)
	armDigest := writeBlob(t, bucketRelDir, arm)

	index := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[
		{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":%d,"platform":{"architecture":"amd64","os":"linux"}},
		{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":%d,"platform":{"architecture":"arm64","os":"linux"}}]}`,
		amdDigest, len(amd), armDigest, len(arm))

	manifestPath := filepath.Join(bucketRelDir, "manifests", "myapp", "v1.0", "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	return bucketRelDir, keyFile, pubFile
}

// TestCopy_MultiArchCarriesSignature is the regression this file exists for.
// The multi-arch branch of copy used to write manifest.json and nothing else, so
// copying a signed multi-arch image produced an unsigned one — and said nothing.
// The image looked fine until something that enforces signatures refused it.
func TestCopy_MultiArchCarriesSignature(t *testing.T) {
	ctx := context.Background()
	_, keyFile, pubFile := setupSignedMultiArchStore(t)

	src := "local://./mystore/myapp:v1.0"
	dest := "local://./mystore/myapp:copied"

	if _, err := Sign(ctx, src, keyFile); err != nil {
		t.Fatalf("sign source: %v", err)
	}
	res, err := Copy(ctx, src, dest, CopyOptions{})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if res.SignaturesDropped != 0 {
		t.Errorf("an unfiltered copy dropped %d signature(s), want 0", res.SignaturesDropped)
	}

	got, err := Verify(ctx, dest, pubFile)
	if err != nil {
		t.Fatalf("verify copy: %v", err)
	}
	if !got.Verified {
		t.Errorf("the copy does not verify: %s", got.Reason)
	}
}

// TestCopy_PlatformFilterDropsSignatureLoudly covers the other half. Filtering
// platforms rewrites the manifest, so the signature over the old bytes cannot
// verify against the new ones. Carrying it would produce an image that claims to
// be signed and fails — worse than one that is plainly unsigned. It must be
// dropped, and it must be counted so the caller can say so.
func TestCopy_PlatformFilterDropsSignatureLoudly(t *testing.T) {
	ctx := context.Background()
	_, keyFile, pubFile := setupSignedMultiArchStore(t)

	src := "local://./mystore/myapp:v1.0"
	dest := "local://./mystore/myapp:amd64only"

	if _, err := Sign(ctx, src, keyFile); err != nil {
		t.Fatalf("sign source: %v", err)
	}
	res, err := Copy(ctx, src, dest, CopyOptions{Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if res.SignaturesDropped != 1 {
		t.Fatalf("dropped %d signature(s), want 1", res.SignaturesDropped)
	}

	got, err := Verify(ctx, dest, pubFile)
	if err == nil && got.Verified {
		t.Error("the filtered copy verified, so a stale signature travelled with it")
	}
}
