package image

import (
	"os"
	"testing"
)

func TestPush_Integration(t *testing.T) {
	if os.Getenv("S3LO_TEST_BUCKET") == "" || os.Getenv("S3LO_TEST_DOCKER") == "" {
		t.Skip("set S3LO_TEST_BUCKET and S3LO_TEST_DOCKER to run integration tests")
	}

	err := Push("alpine:latest", "s3://"+os.Getenv("S3LO_TEST_BUCKET")+"/test-alpine:latest")
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
}

func TestPush_InvalidRef(t *testing.T) {
	err := Push("alpine:latest", "http://invalid/ref")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
