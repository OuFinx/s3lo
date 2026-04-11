package image

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
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
	// OnBlob is called after each blob is downloaded.
	OnBlob func(digest string, size int64)
}

// Scan downloads an image from S3 and scans it for vulnerabilities with Trivy.
// Returns the Trivy exit code (non-zero when vulnerabilities are found at the requested severity),
// and an error for non-Trivy failures (S3, IO, etc.).
func Scan(ctx context.Context, s3Ref string, opts ScanOptions) (int, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return 0, fmt.Errorf("invalid S3 reference: %w", err)
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return 0, fmt.Errorf("create S3 client: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "s3lo-scan-*")
	if err != nil {
		return 0, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Fetch manifest.
	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		return 0, fmt.Errorf("fetch manifest: %w", err)
	}

	// Resolve platform for multi-arch images.
	if isImageIndex(manifestData) {
		manifestData, err = resolvePlatformManifest(ctx, client, parsed.Bucket, manifestData, opts.Platform)
		if err != nil {
			return 0, err
		}
	}

	// Download config + layer blobs to tmpDir/blobs/sha256/.
	if err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob); err != nil {
		return 0, fmt.Errorf("download image: %w", err)
	}

	// Augment tmpDir into a valid OCI Image Layout so Trivy can read it.
	if err := finalizeOCILayout(tmpDir, manifestData); err != nil {
		return 0, fmt.Errorf("build OCI layout: %w", err)
	}

	// Tar the OCI layout into a single file for trivy --input.
	tarFile, err := os.CreateTemp("", "s3lo-scan-*.tar")
	if err != nil {
		return 0, fmt.Errorf("create scan tar: %w", err)
	}
	tarName := tarFile.Name()
	defer os.Remove(tarName)

	if err := tarDirectory(tmpDir, tarFile); err != nil {
		tarFile.Close()
		return 0, fmt.Errorf("tar OCI layout: %w", err)
	}
	tarFile.Close()

	// Build trivy arguments.
	args := []string{"image", "--input", tarName}
	if opts.Severity != "" {
		args = append(args, "--severity", opts.Severity)
	}
	if opts.Format != "" {
		args = append(args, "--format", opts.Format)
	}

	cmd := exec.CommandContext(ctx, opts.TrivyPath, args...)
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

// tarDirectory writes all files in srcDir into w as a flat tar archive.
// File paths in the archive are relative to srcDir.
func tarDirectory(srcDir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if rel == "." {
				return nil
			}
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     rel + "/",
				Mode:     0o755,
			})
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     rel,
			Size:     info.Size(),
			Mode:     0o644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
