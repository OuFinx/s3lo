package s3

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func (c *Client) DownloadDirectory(ctx context.Context, bucket, prefix, destDir string) error {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return err
	}

	keys, err := listKeys(ctx, s3Client, bucket, prefix)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(keys))

	for _, key := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			localPath := buildLocalPath(destDir, prefix, k)
			if err := downloadObject(ctx, s3Client, bucket, k, localPath); err != nil {
				errCh <- fmt.Errorf("download %s: %w", k, err)
			}
		}(key)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		return err
	}
	return nil
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
