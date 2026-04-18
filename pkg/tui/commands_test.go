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

func TestFetchLayerMatrixCmd_BuildsMatrix(t *testing.T) {
	// Shared layer "aaaa…" appears in all three tags.
	// Shared layer "bbbb…" appears in v1.0 and v2.0 only.
	// Each tag also has one unique layer.
	const (
		digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // all 3 tags
		digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" // v1.0 + v2.0
		digestC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" // v1.0 only
		digestD = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" // v2.0 only
		digestE = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" // v3.0 only
	)

	type layerDesc struct {
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
		MediaType string `json:"mediaType"`
	}
	type configDesc struct {
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
		MediaType string `json:"mediaType"`
	}
	type manifest struct {
		SchemaVersion int         `json:"schemaVersion"`
		MediaType     string      `json:"mediaType"`
		Config        configDesc  `json:"config"`
		Layers        []layerDesc `json:"layers"`
	}

	dir := t.TempDir()
	st := storage.NewLocalClient()
	ctx := context.Background()
	bucket := dir

	writeM := func(tagName string, layers []layerDesc) {
		m := manifest{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.manifest.v1+json",
			Config: configDesc{
				Digest:    "sha256:" + digestA[:len(digestA)], // reuse any valid digest for config
				Size:      512,
				MediaType: "application/vnd.oci.image.config.v1+json",
			},
			Layers: layers,
		}
		data, _ := json.Marshal(m)
		key := "manifests/myapp/" + tagName + "/manifest.json"
		if err := st.PutObject(ctx, bucket, key, data); err != nil {
			t.Fatalf("PutObject: %v", err)
		}
	}

	writeM("v1.0", []layerDesc{
		{Digest: "sha256:" + digestA, Size: 50 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
		{Digest: "sha256:" + digestB, Size: 20 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
		{Digest: "sha256:" + digestC, Size: 10 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
	})
	writeM("v2.0", []layerDesc{
		{Digest: "sha256:" + digestA, Size: 50 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
		{Digest: "sha256:" + digestB, Size: 20 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
		{Digest: "sha256:" + digestD, Size: 15 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
	})
	writeM("v3.0", []layerDesc{
		{Digest: "sha256:" + digestA, Size: 50 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
		{Digest: "sha256:" + digestE, Size: 8 << 20, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"},
	})

	tags := []TagEntry{
		{Name: "v1.0"},
		{Name: "v2.0"},
		{Name: "v3.0"},
	}
	cmd := fetchLayerMatrixCmd(ctx, st, bucket, "", "myapp", tags)
	raw := cmd()

	msg, ok := raw.(layerMatrixFetchedMsg)
	if !ok {
		t.Fatalf("expected layerMatrixFetchedMsg, got %T", raw)
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}

	mx := msg.matrix
	if len(mx.Tags) != 3 {
		t.Errorf("expected 3 tag columns, got %d", len(mx.Tags))
	}
	if len(mx.Rows) != 5 {
		t.Errorf("expected 5 unique layers, got %d", len(mx.Rows))
	}

	// First row must be digestA (shared by all 3 tags).
	if mx.Rows[0].Digest != digestA {
		t.Errorf("expected digestA as most-shared row, got %s", mx.Rows[0].Digest[:12])
	}
	if mx.Rows[0].TagCount != 3 {
		t.Errorf("expected TagCount 3 for digestA, got %d", mx.Rows[0].TagCount)
	}

	// Second row must be digestB (shared by 2 tags, larger than C/D/E).
	if mx.Rows[1].Digest != digestB {
		t.Errorf("expected digestB second, got %s", mx.Rows[1].Digest[:12])
	}
	if mx.Rows[1].TagCount != 2 {
		t.Errorf("expected TagCount 2 for digestB, got %d", mx.Rows[1].TagCount)
	}

	// StoredBytes = unique layers once each.
	wantStored := int64((50 + 20 + 10 + 15 + 8) << 20)
	if mx.StoredBytes != wantStored {
		t.Errorf("StoredBytes: want %d, got %d", wantStored, mx.StoredBytes)
	}
	// LogicalBytes counts duplication: A×3 + B×2 + C×1 + D×1 + E×1.
	wantLogical := int64((50*3 + 20*2 + 10 + 15 + 8) << 20)
	if mx.LogicalBytes != wantLogical {
		t.Errorf("LogicalBytes: want %d, got %d", wantLogical, mx.LogicalBytes)
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
