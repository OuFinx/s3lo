package image

import (
	"context"
	"os"
	"testing"
)

func TestPull_Integration(t *testing.T) {
	if os.Getenv("S3LO_TEST_BUCKET") == "" {
		t.Skip("set S3LO_TEST_BUCKET to run integration tests")
	}

	tmpDir := t.TempDir()
	err := Pull(context.Background(), "s3://"+os.Getenv("S3LO_TEST_BUCKET")+"/test-alpine:latest", tmpDir)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
}

func TestPull_InvalidRef(t *testing.T) {
	err := Pull(context.Background(), "http://invalid/ref", t.TempDir())
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
