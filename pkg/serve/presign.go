package serve

import (
	"context"
	"time"
)

// Presigner can generate presigned GET URLs for objects.
// storage.Client (S3/S3-compatible) implements this interface.
// GCS, Azure, and Local backends do not — they fall back to streaming.
type Presigner interface {
	PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}
