package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCopy_TagIsASingleObject pins the contract that makes tag writes atomic.
//
// A tag must be exactly one object. The moment it is two or more, updating a tag
// stops being atomic: a reader can catch the update halfway and see a new
// manifest paired with stale metadata, and an interrupted write leaves the tag
// broken for good. S3 gives no multi-object transaction to fix that afterwards,
// so the only defence is keeping the tag down to a single PutObject.
func TestCopy_TagIsASingleObject(t *testing.T) {
	ctx := context.Background()

	parentDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parentDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	storeDir := filepath.Join(parentDir, "store")
	put := func(content []byte) string {
		sum := sha256.Sum256(content)
		d := hex.EncodeToString(sum[:])
		writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", d), content)
		return d
	}

	config := put([]byte(`{"architecture":"amd64","os":"linux"}`))
	layer := put([]byte("layer contents"))
	platformManifest := fmt.Appendf(nil,
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
			`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":37},`+
			`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:%s","size":14}]}`,
		config, layer)
	platDigest := put(platformManifest)

	// An image index, because that is the branch that used to write a second
	// object next to the manifest.
	index := fmt.Appendf(nil,
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[`+
			`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":%d,`+
			`"platform":{"os":"linux","architecture":"amd64"}}]}`,
		platDigest, len(platformManifest))
	writeTestFile(t, filepath.Join(storeDir, "manifests", "myapp", "v1", "manifest.json"), index)

	if _, err := Copy(ctx, "local://./store/myapp:v1", "local://./store/myapp:v2", CopyOptions{}); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	tagDir := filepath.Join(storeDir, "manifests", "myapp", "v2")
	entries, err := os.ReadDir(tagDir)
	if err != nil {
		t.Fatalf("read tag dir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if !contains(names, "manifest.json") {
		t.Fatalf("tag is missing manifest.json, got %v", names)
	}
	// history.json is separate bookkeeping, not part of the tag's content.
	for _, dead := range []string{"config.json", "index.json", "oci-layout"} {
		if contains(names, dead) {
			t.Errorf("tag still writes %s; a tag must be a single object to update atomically "+
				"(present: %v)", dead, names)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}
