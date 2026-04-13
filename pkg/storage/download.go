package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
)

// ObjectMeta holds key and metadata for an S3 object.
type ObjectMeta struct {
	Key          string
	Size         int64
	LastModified time.Time
	StorageClass string
}

// DownloadDirectory downloads all objects under prefix into destDir.
func (c *Client) DownloadDirectory(ctx context.Context, bucket, prefix, destDir string) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	keys, err := listKeys(ctx, s3Client, bucket, prefix)
	if err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for _, key := range keys {
		key := key
		g.Go(func() error {
			localPath := buildLocalPath(destDir, prefix, key)
			return downloadObject(ctx, s3Client, bucket, key, localPath)
		})
	}

	return g.Wait()
}

// GetObject downloads a single S3 object and returns its contents.
func (c *Client) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}

	resp, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// DownloadObjectToFile downloads a single S3 object to a local file path.
func (c *Client) DownloadObjectToFile(ctx context.Context, bucket, key, localPath string) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	return downloadObject(ctx, s3Client, bucket, key, localPath)
}

// ListKeys returns all S3 object keys under prefix.
func (c *Client) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	return listKeys(ctx, s3Client, bucket, prefix)
}

// ListObjectsWithMeta returns all S3 objects under prefix with size and last-modified metadata.
func (c *Client) ListObjectsWithMeta(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error) {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}

	var objects []ObjectMeta
	paginator := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			meta := ObjectMeta{Key: *obj.Key, StorageClass: string(obj.StorageClass)}
			if obj.Size != nil {
				meta.Size = *obj.Size
			}
			if obj.LastModified != nil {
				meta.LastModified = *obj.LastModified
			}
			objects = append(objects, meta)
		}
	}
	return objects, nil
}

// DeleteObjects deletes multiple S3 objects in batches of up to 1000.
func (c *Client) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	const batchSize = 1000
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]

		objects := make([]s3types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			k := key
			objects[j] = s3types.ObjectIdentifier{Key: &k}
		}

		quiet := true
		_, err := s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &bucket,
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   &quiet,
			},
		})
		if err != nil {
			return fmt.Errorf("delete objects batch: %w", err)
		}
	}
	return nil
}

// CopyObject copies an S3 object within the same bucket.
func (c *Client) CopyObject(ctx context.Context, bucket, srcKey, destKey string) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	copySource := bucket + "/" + srcKey
	_, err = s3Client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     &bucket,
		CopySource: &copySource,
		Key:        &destKey,
	})
	return err
}

func listKeys(ctx context.Context, client *s3.Client, bucket, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			keys = append(keys, *obj.Key)
		}
	}
	return keys, nil
}

func downloadObject(ctx context.Context, client *s3.Client, bucket, key, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}

	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func buildLocalPath(destDir, prefix, key string) string {
	rel := strings.TrimPrefix(key, prefix)
	rel = strings.TrimPrefix(rel, "/")
	return filepath.Join(destDir, rel)
}
