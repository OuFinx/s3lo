package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

func TestCopy_HeadObjectExistsErrorIsPropagated(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	chdirTemp(t, parentDir)

	srcRef := makeSingleArchStore(t, parentDir, "srcstore", "myapp", "v1.0")
	destRef := "local://./dststore/myapp:v1.0"

	blobsDir := filepath.Join(parentDir, "dststore", "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobsDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blobsDir, 0o755)
	})

	_, err := Copy(ctx, srcRef, destRef, CopyOptions{})
	if err == nil {
		t.Fatal("expected copy error, got nil")
	}
	if !strings.Contains(err.Error(), "check destination blob") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecordHistory_PreservesCorruptHistoryFile(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	chdirTemp(t, parentDir)

	client := storage.NewLocalClient()
	parsed, err := ref.Parse("local://./mystore/myapp:v1.0")
	if err != nil {
		t.Fatal(err)
	}

	historyPath := filepath.Join(parentDir, "mystore", "manifests", "myapp", "v1.0", "history.json")
	writeTestFile(t, historyPath, []byte("{bad json"))

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	err = recordHistory(ctx, client, parsed, manifest, 123)
	if err == nil {
		t.Fatal("expected history parse error, got nil")
	}

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{bad json" {
		t.Fatalf("history file was overwritten: %q", data)
	}
}
