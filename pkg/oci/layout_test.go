package oci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestExportDockerImage(t *testing.T) {
	if os.Getenv("S3LO_TEST_DOCKER") == "" {
		t.Skip("skipping docker export test: set S3LO_TEST_DOCKER=1 to enable")
	}

	destDir := t.TempDir()
	layerDescs, manifestBytes, configBytes, err := ExportImage("alpine:latest", destDir)
	if err != nil {
		t.Fatalf("ExportImage: %v", err)
	}

	if len(layerDescs) == 0 {
		t.Error("expected at least one layer descriptor")
	}
	if len(manifestBytes) == 0 {
		t.Error("expected non-empty manifest bytes")
	}
	if len(configBytes) == 0 {
		t.Error("expected non-empty config bytes")
	}

	// Verify blobs exist on disk
	blobsDir := filepath.Join(destDir, "blobs", "sha256")
	for _, desc := range layerDescs {
		digestHex := string(desc.Digest)[len("sha256:"):]
		blobPath := filepath.Join(blobsDir, digestHex)
		if _, err := os.Stat(blobPath); err != nil {
			t.Errorf("layer blob not found at %s: %v", blobPath, err)
		}
	}
}

func TestWriteOCILayout(t *testing.T) {
	dir := t.TempDir()

	// Create a fake blob
	fakeConfig := []byte(`{"architecture":"amd64","os":"linux"}`)
	fakeLayer := []byte("fake layer content")

	manifest := ocispec.Manifest{
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.Digest("sha256:" + sha256Hex(fakeConfig)),
			Size:      int64(len(fakeConfig)),
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    digest.Digest("sha256:" + sha256Hex(fakeLayer)),
				Size:      int64(len(fakeLayer)),
			},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	if err := WriteOCILayout(dir, manifestBytes, fakeConfig); err != nil {
		t.Fatalf("WriteOCILayout: %v", err)
	}

	// Verify required files exist
	for _, fname := range []string{"manifest.json", "config.json", "index.json", ocispec.ImageLayoutFile} {
		path := filepath.Join(dir, fname)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", fname, err)
		}
	}

	// Verify index.json has valid structure
	indexData, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var index ocispec.Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("unmarshal index.json: %v", err)
	}
	if len(index.Manifests) != 1 {
		t.Errorf("expected 1 manifest in index, got %d", len(index.Manifests))
	}

	// Verify oci-layout file
	layoutData, err := os.ReadFile(filepath.Join(dir, ocispec.ImageLayoutFile))
	if err != nil {
		t.Fatalf("read oci-layout: %v", err)
	}
	var layout ocispec.ImageLayout
	if err := json.Unmarshal(layoutData, &layout); err != nil {
		t.Fatalf("unmarshal oci-layout: %v", err)
	}
	if layout.Version != ocispec.ImageLayoutVersion {
		t.Errorf("expected layout version %s, got %s", ocispec.ImageLayoutVersion, layout.Version)
	}
}
