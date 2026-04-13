package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// SBOMOptions controls SBOM generation behavior.
type SBOMOptions struct {
	// Format is the SBOM output format: "cyclonedx" (default), "spdx-json", "spdx".
	Format string
	// Platform selects a specific platform from a multi-arch image (e.g. "linux/amd64").
	Platform string
	// OutputPath writes the SBOM to a file instead of stdout. Empty means stdout.
	OutputPath string
	// TrivyPath is the absolute path to the trivy binary.
	TrivyPath string
	// OnStart is called once with the total blob bytes before downloads begin.
	OnStart func(totalBytes int64)
	// OnBlob is called after each blob is downloaded.
	OnBlob func(digest string, size int64)
}

// SBOM generates a Software Bill of Materials for an image stored in object storage.
// Output is written to opts.OutputPath or stdout if empty.
// Supported formats: cyclonedx (default), spdx-json, spdx.
func SBOM(ctx context.Context, s3Ref string, opts SBOMOptions) error {
	if opts.Format == "" {
		opts.Format = "cyclonedx"
	}

	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return fmt.Errorf("invalid reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "s3lo-sbom-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}

	if isImageIndex(manifestData) {
		manifestData, err = resolvePlatformManifest(ctx, client, parsed.Bucket, manifestData, opts.Platform)
		if err != nil {
			return err
		}
	}

	if err := pullV110(ctx, client, parsed, manifestData, tmpDir, opts.OnBlob, opts.OnStart); err != nil {
		return fmt.Errorf("download image: %w", err)
	}

	if err := finalizeOCILayout(tmpDir, manifestData); err != nil {
		return fmt.Errorf("build OCI layout: %w", err)
	}

	args := []string{"image", "--input", tmpDir, "--format", opts.Format}

	cmd := exec.CommandContext(ctx, opts.TrivyPath, args...)
	if opts.OutputPath != "" {
		f, err := os.Create(opts.OutputPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		cmd.Stdout = f
	} else {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("trivy exited with error: %w", err)
		}
		return fmt.Errorf("run trivy: %w", err)
	}
	return nil
}
