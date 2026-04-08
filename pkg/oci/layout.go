package oci

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/client"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// dockerManifestEntry is one entry from Docker's manifest.json array.
type dockerManifestEntry struct {
	Config   string
	RepoTags []string
	Layers   []string
}

// ExportImage exports a Docker image to an OCI layout directory.
// Returns layer descriptors, manifest bytes, and config bytes.
func ExportImage(imageRef string, destDir string) ([]ocispec.Descriptor, []byte, []byte, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	ctx := context.Background()
	rc, err := cli.ImageSave(ctx, []string{imageRef})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("image save %q: %w", imageRef, err)
	}
	defer rc.Close()

	// Extract tar to temp dir
	tmpDir, err := os.MkdirTemp("", "s3lo-export-*")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := extractTar(rc, tmpDir); err != nil {
		return nil, nil, nil, fmt.Errorf("extract docker tar: %w", err)
	}

	// Read Docker manifest.json
	manifestData, err := os.ReadFile(filepath.Join(tmpDir, "manifest.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read docker manifest.json: %w", err)
	}

	var entries []dockerManifestEntry
	if err := json.Unmarshal(manifestData, &entries); err != nil {
		return nil, nil, nil, fmt.Errorf("parse docker manifest.json: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil, nil, fmt.Errorf("docker manifest.json has no entries")
	}
	entry := entries[0]

	// Read config
	configBytes, err := os.ReadFile(filepath.Join(tmpDir, entry.Config))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read config %q: %w", entry.Config, err)
	}
	configDigest := sha256Hex(configBytes)
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.Digest("sha256:" + configDigest),
		Size:      int64(len(configBytes)),
	}

	// Write blobs dir
	blobsDir := filepath.Join(destDir, "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		return nil, nil, nil, fmt.Errorf("create blobs dir: %w", err)
	}

	// Write config blob
	if err := os.WriteFile(filepath.Join(blobsDir, configDigest), configBytes, 0o644); err != nil {
		return nil, nil, nil, fmt.Errorf("write config blob: %w", err)
	}

	// Process layers
	var layerDescs []ocispec.Descriptor
	for _, layerPath := range entry.Layers {
		layerBytes, err := os.ReadFile(filepath.Join(tmpDir, layerPath))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read layer %q: %w", layerPath, err)
		}
		layerDigest := sha256Hex(layerBytes)
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    digest.Digest("sha256:" + layerDigest),
			Size:      int64(len(layerBytes)),
		}
		layerDescs = append(layerDescs, desc)

		if err := os.WriteFile(filepath.Join(blobsDir, layerDigest), layerBytes, 0o644); err != nil {
			return nil, nil, nil, fmt.Errorf("write layer blob: %w", err)
		}
	}

	// Build OCI manifest
	ociManifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    layerDescs,
	}
	manifestBytes, err := json.Marshal(ociManifest)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal OCI manifest: %w", err)
	}

	return layerDescs, manifestBytes, configBytes, nil
}

// WriteOCILayout writes manifest.json, config.json, and index.json to an OCI layout directory.
func WriteOCILayout(dir string, manifestBytes []byte, configBytes []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create oci layout dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return fmt.Errorf("write manifest.json: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), configBytes, 0o644); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}

	// Write oci-layout file
	ociLayout := ocispec.ImageLayout{Version: ocispec.ImageLayoutVersion}
	ociLayoutBytes, err := json.Marshal(ociLayout)
	if err != nil {
		return fmt.Errorf("marshal oci-layout: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ocispec.ImageLayoutFile), ociLayoutBytes, 0o644); err != nil {
		return fmt.Errorf("write oci-layout: %w", err)
	}

	// Build and write index.json
	manifestDigest := digest.Digest("sha256:" + sha256Hex(manifestBytes))
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    manifestDigest,
				Size:      int64(len(manifestBytes)),
			},
		},
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("marshal index.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), indexBytes, 0o644); err != nil {
		return fmt.Errorf("write index.json: %w", err)
	}

	return nil
}

// extractTar extracts a tar archive from r into dir.
func extractTar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dir, filepath.Clean(hdr.Name))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

// sha256Hex returns the hex-encoded SHA256 digest of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
