package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	gcslib "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// Compile-time assertion that GCSClient implements Backend.
var _ Backend = (*GCSClient)(nil)

// GCSClient implements Backend using Google Cloud Storage.
type GCSClient struct {
	client *gcslib.Client
}

// newGCSBackend creates a GCSClient and returns it as a Backend.
func newGCSBackend(ctx context.Context) (Backend, error) {
	return newGCSClient(ctx)
}

// newGCSClient creates a new GCS client using Application Default Credentials.
func newGCSClient(ctx context.Context) (*GCSClient, error) {
	c, err := gcslib.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return &GCSClient{client: c}, nil
}

func (c *GCSClient) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	rc, err := c.client.Bucket(bucket).Object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, gcslib.ErrObjectNotExist) {
			return nil, &gcsNotFoundError{bucket: bucket, key: key}
		}
		return nil, fmt.Errorf("gcs get %s/%s: %w", bucket, key, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("gcs read %s/%s: %w", bucket, key, err)
	}
	return data, nil
}

func (c *GCSClient) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	wc := c.client.Bucket(bucket).Object(key).NewWriter(ctx)
	if _, err := io.Copy(wc, bytes.NewReader(data)); err != nil {
		_ = wc.Close()
		return fmt.Errorf("gcs write %s/%s: %w", bucket, key, err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("gcs close writer %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *GCSClient) HeadObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	_, err := c.client.Bucket(bucket).Object(key).Attrs(ctx)
	if err != nil {
		if errors.Is(err, gcslib.ErrObjectNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("gcs head %s/%s: %w", bucket, key, err)
	}
	return true, nil
}

func (c *GCSClient) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	it := c.client.Bucket(bucket).Objects(ctx, &gcslib.Query{Prefix: prefix})
	var keys []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcs list %s/%s: %w", bucket, prefix, err)
		}
		keys = append(keys, attrs.Name)
	}
	return keys, nil
}

func (c *GCSClient) ListObjectsWithMeta(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error) {
	it := c.client.Bucket(bucket).Objects(ctx, &gcslib.Query{Prefix: prefix})
	var objects []ObjectMeta
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcs list-meta %s/%s: %w", bucket, prefix, err)
		}
		objects = append(objects, ObjectMeta{
			Key:          attrs.Name,
			Size:         attrs.Size,
			LastModified: attrs.Updated,
		})
	}
	return objects, nil
}

func (c *GCSClient) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	var errs []error
	for _, key := range keys {
		if err := c.client.Bucket(bucket).Object(key).Delete(ctx); err != nil {
			if !errors.Is(err, gcslib.ErrObjectNotExist) {
				errs = append(errs, fmt.Errorf("gcs delete %s/%s: %w", bucket, key, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (c *GCSClient) UploadFile(ctx context.Context, localPath, bucket, key string, sc StorageClass) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	wc := c.client.Bucket(bucket).Object(key).NewWriter(ctx)
	if gcsSC := toGCSStorageClass(sc); gcsSC != "" {
		wc.StorageClass = gcsSC
	}
	if _, err := io.Copy(wc, f); err != nil {
		_ = wc.Close()
		return fmt.Errorf("gcs upload %s → %s/%s: %w", localPath, bucket, key, err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("gcs close writer %s/%s: %w", bucket, key, err)
	}
	return nil
}

// toGCSStorageClass maps our StorageClass to a GCS storage class string.
// Empty return means use the bucket's default class.
func toGCSStorageClass(sc StorageClass) string {
	switch sc {
	case StorageClassStandard:
		return "STANDARD"
	case StorageClassIntelligentTiering:
		return "NEARLINE"
	default:
		return ""
	}
}

func (c *GCSClient) DownloadObjectToFile(ctx context.Context, bucket, key, localPath string) error {
	rc, err := c.client.Bucket(bucket).Object(key).NewReader(ctx)
	if err != nil {
		if errors.Is(err, gcslib.ErrObjectNotExist) {
			return &gcsNotFoundError{bucket: bucket, key: key}
		}
		return fmt.Errorf("gcs open reader %s/%s: %w", bucket, key, err)
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", localPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("gcs download %s/%s → %s: %w", bucket, key, localPath, err)
	}
	return nil
}

func (c *GCSClient) DownloadDirectory(ctx context.Context, bucket, prefix, destDir string) error {
	keys, err := c.ListKeys(ctx, bucket, prefix)
	if err != nil {
		return err
	}
	for _, key := range keys {
		rel, err := filepath.Rel(filepath.FromSlash(prefix), filepath.FromSlash(key))
		if err != nil {
			rel = filepath.FromSlash(key)
		}
		dest := filepath.Join(destDir, rel)
		if err := c.DownloadObjectToFile(ctx, bucket, key, dest); err != nil {
			return err
		}
	}
	return nil
}

func (c *GCSClient) CopyObject(ctx context.Context, bucket, srcKey, destKey string) error {
	src := c.client.Bucket(bucket).Object(srcKey)
	dst := c.client.Bucket(bucket).Object(destKey)
	if _, err := dst.CopierFrom(src).Run(ctx); err != nil {
		return fmt.Errorf("gcs copy %s/%s → %s: %w", bucket, srcKey, destKey, err)
	}
	return nil
}

// gcsNotFoundError signals that an object doesn't exist in GCS.
type gcsNotFoundError struct{ bucket, key string }

func (e *gcsNotFoundError) Error() string {
	return fmt.Sprintf("object not found: gs://%s/%s", e.bucket, e.key)
}
