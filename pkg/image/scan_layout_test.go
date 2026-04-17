package image

import (
	"context"
	"testing"
)

func TestPullToOCILayout_InvalidRef(t *testing.T) {
	_, _, err := PullToOCILayout(context.Background(), "http://not-valid/img:tag", ScanOptions{})
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
