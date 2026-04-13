package storage

import (
	"context"
	"fmt"
)

// GCSClient implements Backend using Google Cloud Storage.
// Full implementation added in a later task.
type GCSClient struct{}

func newGCSBackend(ctx context.Context) (Backend, error) {
	return nil, fmt.Errorf("GCS backend not yet implemented")
}

type gcsNotFoundError struct{ bucket, key string }

func (e *gcsNotFoundError) Error() string {
	return fmt.Sprintf("object not found: gs://%s/%s", e.bucket, e.key)
}
