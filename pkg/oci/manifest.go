package oci

import (
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ParseManifest parses raw JSON bytes into an OCI Manifest.
func ParseManifest(data []byte) (ocispec.Manifest, error) {
	var m ocispec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

// SerializeManifest serializes an OCI Manifest to JSON bytes.
func SerializeManifest(m ocispec.Manifest) ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("serialize manifest: %w", err)
	}
	return data, nil
}
