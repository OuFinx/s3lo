package s3

import (
	"context"
	"errors"
	"strings"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Backend is the storage interface for all s3lo operations.
// *Client (S3) and *LocalClient (filesystem) both implement it.
type Backend interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
	PutObject(ctx context.Context, bucket, key string, data []byte) error
	HeadObjectExists(ctx context.Context, bucket, key string) (bool, error)
	ListKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	ListObjectsWithMeta(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error)
	DeleteObjects(ctx context.Context, bucket string, keys []string) error
	UploadFile(ctx context.Context, localPath, bucket, key string, storageClass s3types.StorageClass) error
	DownloadObjectToFile(ctx context.Context, bucket, key, localPath string) error
	DownloadDirectory(ctx context.Context, bucket, prefix, destDir string) error
	CopyObject(ctx context.Context, bucket, srcKey, destKey string) error
}

// NewBackendFromRef creates the appropriate backend based on the URL scheme.
// "local://" refs → LocalClient; anything else → S3 Client.
func NewBackendFromRef(ctx context.Context, ref string) (Backend, error) {
	if strings.HasPrefix(ref, "local://") {
		return NewLocalClient(), nil
	}
	return NewClient(ctx)
}

// IsNotFound returns true if err is a "not found / 404" error from either backend.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var localNotFound *localNotFoundError
	if errors.As(err, &localNotFound) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "object not found:")
}
