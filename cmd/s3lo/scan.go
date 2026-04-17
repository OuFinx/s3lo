package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var scanCmd = &cobra.Command{
	Use:   "scan <s3-ref>",
	Short: "Scan an image for vulnerabilities with Trivy",
	Long: `Download an image from S3 and scan it for vulnerabilities using Trivy.

Trivy must be installed, or s3lo can install it automatically.
Use --install-trivy to skip the confirmation prompt.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/scan/

  s3lo scan s3://my-bucket/myapp:v1.0
  s3lo scan s3://my-bucket/myapp:v1.0 --severity HIGH,CRITICAL
  s3lo scan s3://my-bucket/myapp:v1.0 --format json
  s3lo scan s3://my-bucket/myapp:v1.0 --platform linux/arm64`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		installFlag, _ := cmd.Flags().GetBool("install-trivy")
		platform, _ := cmd.Flags().GetString("platform")
		severity, _ := cmd.Flags().GetString("severity")
		format, _ := cmd.Flags().GetString("format")

		trivyPath, err := ensureTrivy(cmd.Context(), installFlag)
		if err != nil {
			return err
		}

		fmt.Printf("Scanning %s\n", args[0])
		var bar *progressbar.ProgressBar
		opts := image.ScanOptions{
			Platform:  platform,
			Severity:  severity,
			Format:    format,
			TrivyPath: trivyPath,
			OnStart: func(total int64) {
				bar = newProgressBar("  downloading", total)
			},
			OnBlob: func(_ string, size int64) {
				if bar != nil {
					bar.Add64(size)
				}
			},
		}

		exitCode, err := image.Scan(cmd.Context(), args[0], opts)
		if bar != nil {
			bar.Finish()
		}
		if err != nil {
			return err
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

func init() {
	securityCmd.AddCommand(scanCmd)
	scanCmd.Flags().Bool("install-trivy", false, "Install Trivy automatically without prompting")
	scanCmd.Flags().String("platform", "", `Platform to scan from a multi-arch image (e.g. "linux/amd64")`)
	scanCmd.Flags().String("severity", "", `Severity levels to report, comma-separated (e.g. "HIGH,CRITICAL")`)
	scanCmd.Flags().String("format", "", `Trivy output format: table (default), json, sarif, cyclonedx, etc.`)
}

// ensureTrivy returns the path to the trivy binary, installing it if needed.
// If installTrivy is true, installs without prompting.
func ensureTrivy(ctx context.Context, installTrivy bool) (string, error) {
	// Check PATH.
	if path, err := exec.LookPath("trivy"); err == nil {
		return path, nil
	}

	// Check ~/.local/bin/trivy.
	home, err := os.UserHomeDir()
	if err == nil {
		localPath := filepath.Join(home, ".local", "bin", "trivy")
		if _, err := os.Stat(localPath); err == nil {
			return localPath, nil
		}
	}

	if installTrivy {
		return doInstallTrivy(ctx)
	}

	// In non-TTY environments (CI), fail fast with instructions.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("trivy not found — install it (https://trivy.dev) or run with --install-trivy to auto-install")
	}

	// Interactive: ask user.
	fmt.Print("Trivy is not installed. Install it now to ~/.local/bin/trivy? [Y/n] ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line != "" && line != "y" && line != "yes" {
		return "", fmt.Errorf("trivy not found — install it from https://trivy.dev")
	}

	return doInstallTrivy(ctx)
}

// doInstallTrivy downloads and installs the latest Trivy binary to ~/.local/bin/trivy.
func doInstallTrivy(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	installDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return "", fmt.Errorf("create install dir: %w", err)
	}

	// Resolve latest version from GitHub redirect.
	version, err := latestTrivyVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve trivy version: %w", err)
	}

	// Build download URL.
	platform, err := trivyPlatformSuffix()
	if err != nil {
		return "", err
	}
	// Version has no "v" prefix in archive names.
	bare := strings.TrimPrefix(version, "v")
	url := fmt.Sprintf("https://github.com/aquasecurity/trivy/releases/download/%s/trivy_%s_%s.tar.gz", version, bare, platform)

	fmt.Printf("Installing Trivy %s to %s ...\n", version, filepath.Join(installDir, "trivy"))

	installPath := filepath.Join(installDir, "trivy")
	if err := downloadAndExtractTrivy(ctx, url, installPath); err != nil {
		return "", fmt.Errorf("install trivy: %w", err)
	}

	fmt.Printf("Trivy installed. Add %s to your PATH to use it directly.\n", installDir)
	return installPath, nil
}

// latestTrivyVersion returns the latest Trivy release tag (e.g. "v0.58.2").
func latestTrivyVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://github.com/aquasecurity/trivy/releases/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no redirect from GitHub releases/latest")
	}
	// Location: https://github.com/aquasecurity/trivy/releases/tag/v0.58.2
	parts := strings.Split(loc, "/")
	tag := parts[len(parts)-1]
	if !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("unexpected tag format: %q", tag)
	}
	return tag, nil
}

// trivyPlatformSuffix returns the platform portion of the Trivy archive name.
// Trivy uses: Linux-64bit, Linux-ARM64, macOS-64bit, macOS-ARM64.
func trivyPlatformSuffix() (string, error) {
	var os_, arch string
	switch runtime.GOOS {
	case "linux":
		os_ = "Linux"
	case "darwin":
		os_ = "macOS"
	default:
		return "", fmt.Errorf("unsupported OS for auto-install: %s (install Trivy manually: https://trivy.dev)", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "64bit"
	case "arm64":
		arch = "ARM64"
	default:
		return "", fmt.Errorf("unsupported architecture for auto-install: %s (install Trivy manually: https://trivy.dev)", runtime.GOARCH)
	}
	return os_ + "-" + arch, nil
}

// maxTrivyBinarySize caps the Trivy binary extraction at 500 MB.
const maxTrivyBinarySize = 500 << 20

// downloadAndExtractTrivy downloads a .tar.gz archive and extracts the "trivy" binary to destPath.
func downloadAndExtractTrivy(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name != "trivy" {
			continue
		}
		if hdr.Size > maxTrivyBinarySize {
			return fmt.Errorf("trivy binary too large (%d bytes, max %d)", hdr.Size, maxTrivyBinarySize)
		}
		// Write to a temp file then rename for atomicity.
		tmp, err := os.CreateTemp(filepath.Dir(destPath), "trivy-*")
		if err != nil {
			return err
		}
		if _, err := io.Copy(tmp, io.LimitReader(tr, maxTrivyBinarySize)); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
		tmp.Close()
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		return os.Rename(tmp.Name(), destPath)
	}
	return fmt.Errorf("trivy binary not found in archive")
}
