package oci

import (
	"encoding/json"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestParseManifest(t *testing.T) {
	raw := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest": "sha256:abc123",
			"size": 512
		},
		"layers": [
			{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"digest": "sha256:def456",
				"size": 1024
			}
		]
	}`

	m, err := ParseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	if m.SchemaVersion != 2 {
		t.Errorf("expected schemaVersion 2, got %d", m.SchemaVersion)
	}
	if m.Config.Digest != "sha256:abc123" {
		t.Errorf("expected config digest sha256:abc123, got %s", m.Config.Digest)
	}
	if len(m.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(m.Layers))
	}
	if m.Layers[0].Digest != "sha256:def456" {
		t.Errorf("expected layer digest sha256:def456, got %s", m.Layers[0].Digest)
	}
}

func TestParseManifestInvalidJSON(t *testing.T) {
	_, err := ParseManifest([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestSerializeManifest(t *testing.T) {
	m := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.Digest("sha256:abc123"),
			Size:      512,
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayerGzip,
				Digest:    digest.Digest("sha256:def456"),
				Size:      1024,
			},
		},
	}

	data, err := SerializeManifest(m)
	if err != nil {
		t.Fatalf("SerializeManifest: %v", err)
	}

	var got ocispec.Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal serialized manifest: %v", err)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("expected schemaVersion 2, got %d", got.SchemaVersion)
	}
	if got.Config.Digest != "sha256:abc123" {
		t.Errorf("expected config digest sha256:abc123, got %s", got.Config.Digest)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	original := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Config: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.Digest("sha256:configdigest"),
			Size:      256,
		},
		Layers: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageLayerGzip,
				Digest:    digest.Digest("sha256:layer1digest"),
				Size:      2048,
			},
			{
				MediaType: ocispec.MediaTypeImageLayerGzip,
				Digest:    digest.Digest("sha256:layer2digest"),
				Size:      4096,
			},
		},
	}

	data, err := SerializeManifest(original)
	if err != nil {
		t.Fatalf("SerializeManifest: %v", err)
	}

	parsed, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	if parsed.SchemaVersion != original.SchemaVersion {
		t.Errorf("schemaVersion mismatch: want %d, got %d", original.SchemaVersion, parsed.SchemaVersion)
	}
	if parsed.Config.Digest != original.Config.Digest {
		t.Errorf("config digest mismatch: want %s, got %s", original.Config.Digest, parsed.Config.Digest)
	}
	if len(parsed.Layers) != len(original.Layers) {
		t.Fatalf("layer count mismatch: want %d, got %d", len(original.Layers), len(parsed.Layers))
	}
	for i, l := range parsed.Layers {
		if l.Digest != original.Layers[i].Digest {
			t.Errorf("layer[%d] digest mismatch: want %s, got %s", i, original.Layers[i].Digest, l.Digest)
		}
	}
}
