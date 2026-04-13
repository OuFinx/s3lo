package image

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestSBOMOptionsDefaults(t *testing.T) {
	opts := SBOMOptions{}
	if opts.Format != "" {
		t.Error("expected empty format before defaulting in caller")
	}
	_ = SBOM // compile check
}

func TestSBOMRequiresTrivyPath(t *testing.T) {
	if _, err := exec.LookPath("trivy"); err == nil {
		t.Skip("trivy found in PATH, skipping error path test")
	}
	ctx := context.Background()
	parentDir := t.TempDir()
	oldCwd, _ := os.Getwd()
	_ = os.Chdir(parentDir)
	defer os.Chdir(oldCwd)

	ref := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")

	err := SBOM(ctx, ref, SBOMOptions{
		TrivyPath: "/nonexistent/trivy",
		Format:    "cyclonedx",
	})
	if err == nil {
		t.Error("expected error when trivy binary does not exist")
	}
}

func TestSBOMOptionsHasOutputPath(t *testing.T) {
	opts := SBOMOptions{OutputPath: "/tmp/sbom.json"}
	if opts.OutputPath != "/tmp/sbom.json" {
		t.Errorf("unexpected output path: %s", opts.OutputPath)
	}
}
