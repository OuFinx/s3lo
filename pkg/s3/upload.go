package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
)

// UploadDirectory uploads all files in localDir to bucket at prefix/ with Standard storage class.
func (c *Client) UploadDirectory(ctx context.Context, localDir, bucket, prefix string) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	var files []string
	err = filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk directory %s: %w", localDir, err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for _, file := range files {
		file := file
		g.Go(func() error {
			key := buildS3Key(prefix, localDir, file)
			return uploadFile(ctx, s3Client, bucket, key, file, "")
		})
	}

	return g.Wait()
}

// UploadFile uploads a single local file to a specific S3 key.
// Pass empty string for storageClass to use the bucket default (Standard).
func (c *Client) UploadFile(ctx context.Context, localPath, bucket, key string, storageClass s3types.StorageClass) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	return uploadFile(ctx, s3Client, bucket, key, localPath, storageClass)
}

func uploadFile(ctx context.Context, client *s3.Client, bucket, key, localPath string, storageClass s3types.StorageClass) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	ct := contentTypeForKey(key)
	input := &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &key,
		Body:        f,
		ContentType: &ct,
	}
	if storageClass != "" {
		input.StorageClass = storageClass
	}
	_, err = client.PutObject(ctx, input)
	return err
}

// contentTypeForKey returns the appropriate Content-Type for an S3 key.
func contentTypeForKey(key string) string {
	switch {
	case strings.HasSuffix(key, ".json") || strings.HasSuffix(key, "manifest.json"):
		return "application/json"
	case strings.HasSuffix(key, "oci-layout"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// PutObject uploads raw bytes to an S3 key.
func (c *Client) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	size := int64(len(data))
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &bucket,
		Key:           &key,
		Body:          bytes.NewReader(data),
		ContentLength: &size,
	})
	return err
}

// HeadObjectExists returns true if an S3 object exists, false if it doesn't (404).
// Returns an error for non-404 failures (permissions, throttling, network).
func (c *Client) HeadObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return false, err
	}
	_, err = s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		// Check if the error is a 404 (NotFound).
		var notFound *s3types.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		// HeadObject returns smithy OperationError wrapping HTTP 404
		// which may not always map to s3types.NotFound. Check for
		// "NotFound" or "404" in the error message as a fallback.
		errMsg := err.Error()
		if strings.Contains(errMsg, "NotFound") || strings.Contains(errMsg, "404") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func buildS3Key(prefix, baseDir, localPath string) string {
	rel, err := filepath.Rel(baseDir, localPath)
	if err != nil {
		rel = filepath.Base(localPath)
	}
	rel = filepath.ToSlash(rel)
	return strings.TrimSuffix(prefix, "/") + "/" + rel
}
