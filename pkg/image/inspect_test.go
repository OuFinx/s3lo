package image

import (
	"os"
	"testing"
)

func TestInspect_Integration(t *testing.T) {
	bucket := os.Getenv("S3LO_TEST_BUCKET")
	if bucket == "" {
		t.Skip("set S3LO_TEST_BUCKET to run integration tests")
	}

	info, err := Inspect("s3://" + bucket + "/test-alpine:latest")
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	out, err := info.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON failed: %v", err)
	}
	t.Log(out)
}

func TestInspect_InvalidRef(t *testing.T) {
	_, err := Inspect("http://not-s3/image:tag")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
