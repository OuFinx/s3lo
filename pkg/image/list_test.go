package image

import (
	"context"
	"testing"
)

func TestList_InvalidRef(t *testing.T) {
	_, err := List(context.Background(), "http://not-s3/bucket/")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
