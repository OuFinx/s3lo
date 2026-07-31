package image

import (
	"context"
	"testing"
)

func TestPull_InvalidRef(t *testing.T) {
	_, err := Pull(context.Background(), "http://invalid/ref", "", PullOptions{})
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
