package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestE2E_PushPullListInspect(t *testing.T) {
	if os.Getenv("S3LO_TEST_BUCKET") == "" || os.Getenv("S3LO_TEST_DOCKER") == "" {
		t.Skip("set S3LO_TEST_BUCKET and S3LO_TEST_DOCKER for e2e tests")
	}

	bucket := os.Getenv("S3LO_TEST_BUCKET")
	binary := "./s3lo"

	// Build
	build := exec.Command("go", "build", "-o", binary, "./cmd/s3lo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %s\n%s", err, out)
	}
	defer os.Remove(binary)

	ref := "s3://" + bucket + "/e2e-test/alpine:test"

	// Push
	push := exec.Command(binary, "push", "alpine:latest", ref)
	if out, err := push.CombinedOutput(); err != nil {
		t.Fatalf("push failed: %s\n%s", err, out)
	}

	// List
	list := exec.Command(binary, "list", "s3://"+bucket+"/")
	listOut, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("list failed: %s\n%s", err, listOut)
	}
	if !strings.Contains(string(listOut), "e2e-test/alpine") {
		t.Errorf("list output doesn't contain pushed image: %s", listOut)
	}

	// Inspect
	inspect := exec.Command(binary, "inspect", ref)
	inspectOut, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %s\n%s", err, inspectOut)
	}
	if !strings.Contains(string(inspectOut), "Layers:") {
		t.Errorf("inspect output missing layers info: %s", inspectOut)
	}

	// Pull (imports into local Docker; no image-tag arg means it uses the ref name)
	pull := exec.Command(binary, "pull", ref)
	if out, err := pull.CombinedOutput(); err != nil {
		t.Fatalf("pull failed: %s\n%s", err, out)
	}
}
