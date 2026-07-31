package oci

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/image"
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

// LocalPlatforms reports the platforms of imageRef that the daemon actually
// holds, as "os/arch" or "os/arch/variant".
//
// It returns nil when the daemon has no per-platform view of the image, which is
// the normal case: the classic image store keeps one platform per tag, and only
// the containerd image store can hold several. Attestation manifests are
// excluded — buildx attaches SBOM and provenance as manifests with platform
// "unknown/unknown", and publishing those as platforms would be nonsense.
func LocalPlatforms(ctx context.Context, imageRef string) ([]string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	resp, err := cli.ImageInspect(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("inspect image %q: %w", imageRef, err)
	}

	var platforms []string
	for _, m := range resp.Manifests {
		if m.Kind != image.ManifestKindImage || !m.Available || m.ImageData == nil {
			continue
		}
		platforms = append(platforms, PlatformString(m.ImageData.Platform))
	}
	return platforms, nil
}

// PlatformString renders an OCI platform the way the CLI accepts it.
func PlatformString(p ocispec.Platform) string {
	s := p.OS + "/" + p.Architecture
	if p.Variant != "" {
		s += "/" + p.Variant
	}
	return s
}

// ExportImage exports one platform of a Docker image to an OCI layout directory.
// Returns layer descriptors, manifest bytes, and config bytes.
//
// platform selects which one when the daemon holds several; empty means let the
// daemon choose, which is what it did before this was selectable.
func ExportImage(ctx context.Context, imageRef, destDir, platform string) ([]ocispec.Descriptor, []byte, []byte, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	var saveOpts []client.ImageSaveOption
	if platform != "" {
		p, err := ParsePlatform(platform)
		if err != nil {
			return nil, nil, nil, err
		}
		saveOpts = append(saveOpts, client.ImageSaveWithPlatforms(p))
	}

	rc, err := cli.ImageSave(ctx, []string{imageRef}, saveOpts...)
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
		srcPath := filepath.Join(tmpDir, layerPath)
		layerDigest, layerSize, err := copyWithHash(srcPath, blobsDir)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("process layer %q: %w", layerPath, err)
		}
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    digest.Digest("sha256:" + layerDigest),
			Size:      layerSize,
		}
		layerDescs = append(layerDescs, desc)
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

// maxExtractFileSize caps individual file sizes during tar extraction (10 GB).
const maxExtractFileSize = 10 << 30

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
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), filepath.Clean(dir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes destination directory", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size > maxExtractFileSize {
				return fmt.Errorf("tar entry %q is too large (%d bytes, max %d)", hdr.Name, hdr.Size, maxExtractFileSize)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxExtractFileSize)); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

// copyWithHash copies a file to blobsDir named by its SHA256 digest, streaming to avoid OOM.
func copyWithHash(srcPath, blobsDir string) (string, int64, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return "", 0, err
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp(blobsDir, "blob-*")
	if err != nil {
		return "", 0, err
	}

	h := sha256.New()
	size, err := io.Copy(tmpFile, io.TeeReader(src, h))
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", 0, err
	}
	tmpFile.Close()

	hexDigest := fmt.Sprintf("%x", h.Sum(nil))
	finalPath := filepath.Join(blobsDir, hexDigest)
	if err := os.Rename(tmpFile.Name(), finalPath); err != nil {
		os.Remove(tmpFile.Name())
		return "", 0, err
	}
	return hexDigest, size, nil
}

// sha256Hex returns the hex-encoded SHA256 digest of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// ImportImage loads an OCI image from a local directory into the Docker daemon.
// It creates a Docker-compatible tar from the OCI layout and calls docker load.
func ImportImage(ctx context.Context, srcDir string, imageRef string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	// Read manifest to find config and layers
	manifestData, err := os.ReadFile(filepath.Join(srcDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read manifest.json: %w", err)
	}

	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	// Build a Docker-format tar in memory/tempfile
	// Docker expects:
	//   manifest.json — array of [{Config: "config.json", RepoTags: ["ref"], Layers: ["layer1/layer.tar", ...]}]
	//   config.json — the config blob
	//   <hash>/layer.tar — each layer

	tmpFile, err := os.CreateTemp("", "s3lo-import-*.tar")
	if err != nil {
		return fmt.Errorf("create temp tar: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	tw := tar.NewWriter(tmpFile)

	// Write config blob
	configDigest := manifest.Config.Digest.Encoded()
	configPath := filepath.Join(srcDir, "blobs", "sha256", configDigest)
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config blob: %w", err)
	}
	configName := configDigest + ".json"
	if err := tarWriteFile(tw, configName, configData); err != nil {
		return fmt.Errorf("write config to tar: %w", err)
	}

	// Write layers
	var layerNames []string
	for _, layer := range manifest.Layers {
		layerDigest := layer.Digest.Encoded()
		layerPath := filepath.Join(srcDir, "blobs", "sha256", layerDigest)

		layerName := layerDigest + "/layer.tar"

		// Stream layer from file instead of reading entirely into memory.
		layerFile, err := os.Open(layerPath)
		if err != nil {
			return fmt.Errorf("open layer blob %s: %w", layerDigest[:12], err)
		}
		layerInfo, err := layerFile.Stat()
		if err != nil {
			layerFile.Close()
			return fmt.Errorf("stat layer blob %s: %w", layerDigest[:12], err)
		}
		hdr := &tar.Header{
			Name: layerName,
			Mode: 0o644,
			Size: layerInfo.Size(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			layerFile.Close()
			return fmt.Errorf("write layer tar header %s: %w", layerDigest[:12], err)
		}
		if _, err := io.Copy(tw, layerFile); err != nil {
			layerFile.Close()
			return fmt.Errorf("write layer to tar %s: %w", layerDigest[:12], err)
		}
		layerFile.Close()

		layerNames = append(layerNames, layerName)
	}

	// Write Docker manifest.json
	dockerManifest := []dockerManifestEntry{
		{
			Config:   configName,
			RepoTags: []string{imageRef},
			Layers:   layerNames,
		},
	}
	dockerManifestData, err := json.Marshal(dockerManifest)
	if err != nil {
		return fmt.Errorf("marshal docker manifest: %w", err)
	}
	if err := tarWriteFile(tw, "manifest.json", dockerManifestData); err != nil {
		return fmt.Errorf("write manifest to tar: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}

	// Seek to start and docker load
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek tar: %w", err)
	}

	resp, err := cli.ImageLoad(ctx, tmpFile)
	if err != nil {
		return fmt.Errorf("docker load: %w", err)
	}
	defer resp.Body.Close()
	return checkLoadResponse(resp.Body)
}

// checkLoadResponse reads the JSON-lines progress stream docker returns from an
// image load and surfaces the first error it reports.
//
// The daemon signals a failed load in the body, not in the HTTP status, so
// discarding the body — as this did — made every failure look like a success:
// `s3lo pull` printed "Done. Image imported into Docker." and exited 0 having
// imported nothing.
func checkLoadResponse(body io.Reader) error {
	dec := json.NewDecoder(body)
	for {
		var msg struct {
			Error       string `json:"error"`
			ErrorDetail *struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// A body we cannot parse is not evidence of success.
			return fmt.Errorf("docker load: reading daemon response: %w", err)
		}
		if msg.ErrorDetail != nil && msg.ErrorDetail.Message != "" {
			return fmt.Errorf("docker load: %s", msg.ErrorDetail.Message)
		}
		if msg.Error != "" {
			return fmt.Errorf("docker load: %s", msg.Error)
		}
	}
}

func tarWriteFile(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// ParsePlatform turns "linux/arm64" or "linux/arm/v7" into an OCI platform.
func ParsePlatform(s string) (ocispec.Platform, error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 2:
		return ocispec.Platform{OS: parts[0], Architecture: parts[1]}, nil
	case 3:
		return ocispec.Platform{OS: parts[0], Architecture: parts[1], Variant: parts[2]}, nil
	default:
		return ocispec.Platform{}, fmt.Errorf("invalid platform %q: want os/arch or os/arch/variant", s)
	}
}
