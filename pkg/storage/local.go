package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalClient implements Backend using the local filesystem.
// The "bucket" parameter in every method is the storage root directory.
type LocalClient struct{}

// NewLocalClient returns a LocalClient. No configuration needed — the bucket path
// serves as the storage root on every method call.
func NewLocalClient() *LocalClient { return &LocalClient{} }

func (c *LocalClient) path(bucket, key string) string {
	return filepath.Join(bucket, filepath.FromSlash(key))
}

// safePath resolves key relative to bucket and rejects any path that escapes the bucket root.
func (c *LocalClient) safePath(bucket, key string) (string, error) {
	absRoot, err := filepath.Abs(bucket)
	if err != nil {
		return "", fmt.Errorf("resolve bucket path: %w", err)
	}
	absP, err := filepath.Abs(filepath.Join(bucket, filepath.FromSlash(key)))
	if err != nil {
		return "", fmt.Errorf("resolve key path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absP)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("key %q escapes bucket root", key)
	}
	return absP, nil
}

func (c *LocalClient) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	p, err := c.safePath(bucket, key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &localNotFoundError{path: p}
		}
		return nil, err
	}
	return data, nil
}

func (c *LocalClient) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	p, err := c.safePath(bucket, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (c *LocalClient) HeadObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	p, err := c.safePath(bucket, key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *LocalClient) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	root := filepath.Join(bucket, filepath.FromSlash(prefix))
	var keys []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(bucket, p)
			keys = append(keys, filepath.ToSlash(rel))
		}
		return nil
	})
	return keys, err
}

func (c *LocalClient) ListObjectsWithMeta(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error) {
	root := filepath.Join(bucket, filepath.FromSlash(prefix))
	var objects []ObjectMeta
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(bucket, p)
			objects = append(objects, ObjectMeta{
				Key:          filepath.ToSlash(rel),
				Size:         info.Size(),
				LastModified: info.ModTime(),
				StorageClass: string(StorageClassStandard),
			})
		}
		return nil
	})
	return objects, err
}

func (c *LocalClient) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	for _, key := range keys {
		p, err := c.safePath(bucket, key)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete %s: %w", p, err)
		}
	}
	return nil
}

func (c *LocalClient) UploadFile(_ context.Context, localPath, bucket, key string, _ StorageClass) error {
	dest, err := c.safePath(bucket, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return localCopyFile(localPath, dest)
}

func (c *LocalClient) DownloadObjectToFile(_ context.Context, bucket, key, localPath string) error {
	src, err := c.safePath(bucket, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	return localCopyFile(src, localPath)
}

func (c *LocalClient) DownloadDirectory(_ context.Context, bucket, prefix, destDir string) error {
	root := filepath.Join(bucket, filepath.FromSlash(prefix))
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		dest := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return localCopyFile(p, dest)
	})
}

func (c *LocalClient) CopyObject(_ context.Context, bucket, srcKey, destKey string) error {
	src, err := c.safePath(bucket, srcKey)
	if err != nil {
		return err
	}
	dest, err := c.safePath(bucket, destKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return localCopyFile(src, dest)
}

func localCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// localNotFoundError signals that an object doesn't exist in local storage.
type localNotFoundError struct{ path string }

func (e *localNotFoundError) Error() string {
	return fmt.Sprintf("object not found: %s", e.path)
}
