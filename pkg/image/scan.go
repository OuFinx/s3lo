package image

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// ScanOptions controls scan behavior.
type ScanOptions struct {
	// Platform selects a specific platform from a multi-arch image (e.g. "linux/amd64").
	// Empty means auto-detect the host platform.
	Platform string
	// Severity filters by severity level (comma-separated: "HIGH,CRITICAL").
	// Empty means Trivy default (all severities).
	Severity string
	// Format controls Trivy output format (table, json, sarif, cyclonedx, etc.).
	// Empty means Trivy default (table).
	Format string
	// TrivyPath is the absolute path to the trivy binary.
	TrivyPath string
	// OnStart is called once with the total blob bytes before any downloads begin.
	OnStart func(totalBytes int64)
	// OnBlob is called after each blob is downloaded.
	OnBlob func(digest string, size int64)
}

// PullToOCILayout downloads an image from S3 and prepares a temporary OCI image layout
// directory for Trivy. Returns (trivyPath, tmpDir, error). The caller must call
// os.RemoveAll(tmpDir) when done.
func PullToOCILayout(ctx context.Context, s3Ref string, opts ScanOptions) (trivyPath, tmpDir string, err error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return "", "", fmt.Errorf("invalid S3 reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return "", "", fmt.Errorf("create storage client: %w", err)
	}

	tmpDir, err = os.MkdirTemp("", "s3lo-scan-*")
	if err != nil {
		return "", "", fmt.Errorf("create temp dir: %w", err)
	}

	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("fetch manifest: %w", err)
	}

	if isImageIndex(manifestData) {
		manifestData, err = resolvePlatformManifest(ctx, client, parsed.Bucket, manifestData, opts.Platform)
		if err != nil {
			os.RemoveAll(tmpDir)
			return "", "", err
		}
	}

	if err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob, opts.OnStart); err != nil {
		os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("download image: %w", err)
	}

	if err := finalizeOCILayout(tmpDir, manifestData); err != nil {
		os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("build OCI layout: %w", err)
	}

	tp := opts.TrivyPath
	if tp == "" {
		tp, err = exec.LookPath("trivy")
		if err != nil {
			os.RemoveAll(tmpDir)
			return "", "", fmt.Errorf("trivy not found in PATH: %w", err)
		}
	}

	return tp, tmpDir, nil
}

// Scan downloads an image from S3 and scans it for vulnerabilities with Trivy.
// Returns the Trivy exit code (non-zero when vulnerabilities are found at the requested severity),
// and an error for non-Trivy failures (S3, IO, etc.).
func Scan(ctx context.Context, s3Ref string, opts ScanOptions) (int, error) {
	trivyPath, tmpDir, err := PullToOCILayout(ctx, s3Ref, opts)
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmpDir)

	// Build trivy arguments.
	// Pass the OCI Image Layout directory directly — Trivy reads index.json from it.
	args := []string{"image", "--input", tmpDir}
	if opts.Severity != "" {
		args = append(args, "--severity", opts.Severity)
	}
	if opts.Format != "" {
		args = append(args, "--format", opts.Format)
	}

	cmd := exec.CommandContext(ctx, trivyPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 0, fmt.Errorf("run trivy: %w", err)
	}
	return 0, nil
}

// finalizeOCILayout adds oci-layout, index.json, and the manifest blob to dir.
// dir already contains manifest.json and blobs/sha256/ from pullV110.
func finalizeOCILayout(dir string, manifestData []byte) error {
	blobsDir := filepath.Join(dir, "blobs", "sha256")

	// Write oci-layout marker.
	ociLayout := ocispec.ImageLayout{Version: ocispec.ImageLayoutVersion}
	ociLayoutBytes, _ := json.Marshal(ociLayout)
	if err := os.WriteFile(filepath.Join(dir, ocispec.ImageLayoutFile), ociLayoutBytes, 0o644); err != nil {
		return fmt.Errorf("write oci-layout: %w", err)
	}

	// Write manifest as a blob so Trivy can find it by digest.
	h := sha256.Sum256(manifestData)
	manifestDigest := fmt.Sprintf("%x", h)
	if err := os.WriteFile(filepath.Join(blobsDir, manifestDigest), manifestData, 0o644); err != nil {
		return fmt.Errorf("write manifest blob: %w", err)
	}

	// Write index.json pointing at the manifest blob.
	idx := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    digest.Digest("sha256:" + manifestDigest),
				Size:      int64(len(manifestData)),
			},
		},
	}
	indexBytes, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), indexBytes, 0o644); err != nil {
		return fmt.Errorf("write index.json: %w", err)
	}

	return nil
}

