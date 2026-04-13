package storage

import (
	"context"
	"errors"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// StorageClass controls blob upload tiering. Backends map this to their own concepts.
type StorageClass string

const (
	StorageClassStandard           StorageClass = "STANDARD"
	StorageClassIntelligentTiering StorageClass = "INTELLIGENT_TIERING"
)

// Backend is the storage interface for all s3lo operations.
// *Client (S3/S3-compatible), *LocalClient, *GCSClient, *AzureClient all implement it.
type Backend interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
	PutObject(ctx context.Context, bucket, key string, data []byte) error
	HeadObjectExists(ctx context.Context, bucket, key string) (bool, error)
	ListKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	ListObjectsWithMeta(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error)
	DeleteObjects(ctx context.Context, bucket string, keys []string) error
	UploadFile(ctx context.Context, localPath, bucket, key string, sc StorageClass) error
	DownloadObjectToFile(ctx context.Context, bucket, key, localPath string) error
	DownloadDirectory(ctx context.Context, bucket, prefix, destDir string) error
	CopyObject(ctx context.Context, bucket, srcKey, destKey string) error
}

type endpointKey struct{}

// WithEndpoint stores a custom endpoint in the context for S3-compatible backends.
func WithEndpoint(ctx context.Context, endpoint string) context.Context {
	return context.WithValue(ctx, endpointKey{}, endpoint)
}

// endpointFromContext retrieves the custom endpoint from context; empty if not set.
func endpointFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(endpointKey{}).(string); ok {
		return v
	}
	return ""
}

// NewBackendFromRef creates the appropriate backend based on the URL scheme.
//   - local://  → LocalClient (filesystem)
//   - gs://     → GCSClient (Google Cloud Storage)
//   - az://     → AzureClient (Azure Blob Storage)
//   - s3:// (default) → S3 Client; uses custom endpoint from context for S3-compatible backends
func NewBackendFromRef(ctx context.Context, ref string) (Backend, error) {
	endpoint := endpointFromContext(ctx)
	switch {
	case strings.HasPrefix(ref, "local://"):
		return NewLocalClient(), nil
	case strings.HasPrefix(ref, "gs://"):
		return newGCSBackend(ctx)
	case strings.HasPrefix(ref, "az://"):
		return newAzureBackend(ctx)
	default:
		return newS3Client(ctx, endpoint)
	}
}

// IsNotFound returns true if err represents a "not found / 404" error from any backend.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	// S3 errors
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	// Local and GCS not-found sentinel errors
	var localNotFound *localNotFoundError
	if errors.As(err, &localNotFound) {
		return true
	}
	var gcsNotFound *gcsNotFoundError
	if errors.As(err, &gcsNotFound) {
		return true
	}
	var azureNotFound *azureNotFoundError
	if errors.As(err, &azureNotFound) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "object not found:")
}
