package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

func setupLocalStore(t *testing.T) (storage.Backend, string, string) {
	t.Helper()
	dir := t.TempDir()
	st := storage.NewLocalClient()
	ctx := context.Background()
	bucket := dir
	prefix := ""

	type fakeLayer struct {
		Size      int64  `json:"size"`
		MediaType string `json:"mediaType"`
	}
	type fakeConfig struct {
		Size      int64  `json:"size"`
		MediaType string `json:"mediaType"`
	}
	type fakeManifest struct {
		SchemaVersion int        `json:"schemaVersion"`
		MediaType     string     `json:"mediaType"`
		Config        fakeConfig `json:"config"`
		Layers        []fakeLayer `json:"layers"`
	}

	writeManifest := func(imageName, tagName string, layerSize int64) {
		m := fakeManifest{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.manifest.v1+json",
			Config:        fakeConfig{Size: 1024, MediaType: "application/vnd.oci.image.config.v1+json"},
			Layers:        []fakeLayer{{Size: layerSize, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"}},
		}
		data, _ := json.Marshal(m)
		key := prefix + "manifests/" + imageName + "/" + tagName + "/manifest.json"
		if err := st.PutObject(ctx, bucket, key, data); err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
	}

	writeManifest("myapp", "v1.0", 100<<20)
	writeManifest("nginx", "latest", 50<<20)

	// v2.0 written slightly later so it has a newer timestamp.
	time.Sleep(20 * time.Millisecond)
	writeManifest("myapp", "v2.0", 120<<20)

	// Write a fake blob.
	if err := st.PutObject(ctx, bucket, prefix+"blobs/sha256/abc123", make([]byte, 1024)); err != nil {
		t.Fatalf("write blob: %v", err)
	}

	return st, bucket, prefix
}

func TestFetchImagesCmd_ReturnsEntries(t *testing.T) {
	st, bucket, prefix := setupLocalStore(t)
	ctx := context.Background()

	cmd := fetchImagesCmd(ctx, st, bucket, prefix)
	msg := cmd()

	fetched, ok := msg.(imagesFetchedMsg)
	if !ok {
		t.Fatalf("expected imagesFetchedMsg, got %T", msg)
	}
	if fetched.err != nil {
		t.Fatalf("unexpected error: %v", fetched.err)
	}
	if len(fetched.entries) != 2 {
		t.Errorf("expected 2 images, got %d", len(fetched.entries))
	}
	if fetched.entries[0].Name != "myapp" {
		t.Errorf("expected first entry 'myapp' (sorted), got %q", fetched.entries[0].Name)
	}
	if fetched.entries[0].TagCount != 2 {
		t.Errorf("expected 2 tags for myapp, got %d", fetched.entries[0].TagCount)
	}
	if fetched.entries[0].TotalBytes == 0 {
		t.Error("expected non-zero TotalBytes for myapp")
	}
}

func TestFetchTagsCmd_ReturnsSortedNewestFirst(t *testing.T) {
	st, bucket, prefix := setupLocalStore(t)
	ctx := context.Background()

	cmd := fetchTagsCmd(ctx, st, bucket, prefix, "myapp")
	msg := cmd()

	fetched, ok := msg.(tagsFetchedMsg)
	if !ok {
		t.Fatalf("expected tagsFetchedMsg, got %T", msg)
	}
	if fetched.err != nil {
		t.Fatalf("unexpected error: %v", fetched.err)
	}
	if len(fetched.tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(fetched.tags))
	}
	if fetched.tags[0].Name != "v2.0" {
		t.Errorf("expected newest tag 'v2.0' first, got %q", fetched.tags[0].Name)
	}
}

func TestFetchImagesCmd_EmptyBucket(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	st := storage.NewLocalClient()
	cmd := fetchImagesCmd(context.Background(), st, dir, "")
	msg := cmd()
	fetched, ok := msg.(imagesFetchedMsg)
	if !ok {
		t.Fatalf("expected imagesFetchedMsg, got %T", msg)
	}
	if fetched.err != nil {
		t.Fatalf("unexpected error for empty bucket: %v", fetched.err)
	}
	if len(fetched.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(fetched.entries))
	}
}
