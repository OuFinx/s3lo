package image

import (
	"os"
	"testing"
)

func TestList_Integration(t *testing.T) {
	bucket := os.Getenv("S3LO_TEST_BUCKET")
	if bucket == "" {
		t.Skip("set S3LO_TEST_BUCKET to run integration tests")
	}

	entries, err := List("s3://" + bucket + "/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	t.Logf("Found %d images", len(entries))
	for _, e := range entries {
		t.Logf("  %s: %v", e.Name, e.Tags)
	}
}

func TestList_InvalidRef(t *testing.T) {
	_, err := List("http://not-s3/bucket/")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
