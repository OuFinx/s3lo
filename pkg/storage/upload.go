package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"golang.org/x/sync/errgroup"
)

// toAWSStorageClass maps our StorageClass type to the AWS SDK type for internal use.
func toAWSStorageClass(sc StorageClass) s3types.StorageClass {
	if sc == StorageClassIntelligentTiering {
		return s3types.StorageClassIntelligentTiering
	}
	return s3types.StorageClassStandard
}

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
func (c *Client) UploadFile(ctx context.Context, localPath, bucket, key string, sc StorageClass) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}
	// Storage classes are an AWS S3 concept. MinIO, Ceph and R2 reject
	// INTELLIGENT_TIERING outright with InvalidStorageClass, so against a custom
	// endpoint the class is dropped and the service's own default applies.
	if c.endpoint != "" {
		return uploadFile(ctx, s3Client, bucket, key, localPath, "")
	}
	return uploadFile(ctx, s3Client, bucket, key, localPath, toAWSStorageClass(sc))
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
	_, err = newUploader(client).Upload(ctx, input)
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
		if s3NotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// s3NotFound reports whether err is S3 saying the object is not there, as
// opposed to any other reason the request failed.
//
// This has to be decided on the typed error. It used to be decided by looking
// for "404" or "NotFound" anywhere in the error text, and AWS puts the object
// key into that text — so for the roughly one sha256 digest in sixty-six that
// contains "404", a throttle, an AccessDenied or a 500 all read as "the object
// is not there". Callers act on that answer: push skips the upload it should
// have retried, and the immutability check concludes the tag is free.
//
// GCS and Azure already decide this from typed errors; S3 was the outlier.
func s3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	// HeadObject has no response body, so the SDK cannot always map it to a
	// typed error. The status line is still authoritative.
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode() == http.StatusNotFound
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey":
			return true
		}
	}
	return false
}

func buildS3Key(prefix, baseDir, localPath string) string {
	rel, err := filepath.Rel(baseDir, localPath)
	if err != nil {
		rel = filepath.Base(localPath)
	}
	rel = filepath.ToSlash(rel)
	return strings.TrimSuffix(prefix, "/") + "/" + rel
}
