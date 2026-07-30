package image

import (
	"context"
	"testing"
)

func TestInspect_InvalidRef(t *testing.T) {
	_, err := Inspect(context.Background(), "http://not-s3/image:tag")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
}
