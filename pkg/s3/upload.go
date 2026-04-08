package s3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/sync/errgroup"
)

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
			return uploadFile(ctx, s3Client, bucket, key, file)
		})
	}

	return g.Wait()
}

func uploadFile(ctx context.Context, client *s3.Client, bucket, key, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   f,
	})
	return err
}

func buildS3Key(prefix, baseDir, localPath string) string {
	rel, err := filepath.Rel(baseDir, localPath)
	if err != nil {
		rel = filepath.Base(localPath)
	}
	rel = filepath.ToSlash(rel)
	return strings.TrimSuffix(prefix, "/") + "/" + rel
}
