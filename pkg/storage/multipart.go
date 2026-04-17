package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// partRetryDelays defines the wait between successive retry attempts for a single part.
// 3 retries (after the initial attempt) with exponential backoff: 1 s, 2 s, 4 s.
var partRetryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

const (
	// multipartThreshold is the file size above which multipart upload is used.
	// S3 PutObject has a hard 5 GB limit; we start using multipart at 100 MB for
	// better throughput and per-part retry granularity.
	multipartThreshold = 100 << 20 // 100 MB

	// multipartPartSize is the size of each part in a multipart upload.
	// Must be >= 5 MB (S3 minimum). 64 MB balances part count and memory use.
	multipartPartSize = 64 << 20 // 64 MB
)

// uploadFileMultipart uploads localPath to S3 using the multipart upload API.
// It reads the file in multipartPartSize chunks, uploading each as a separate part.
// On any failure the in-progress upload is aborted to avoid leaving dangling state.
func uploadFileMultipart(ctx context.Context, client *s3.Client, bucket, key, localPath string, storageClass s3types.StorageClass) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	ct := contentTypeForKey(key)
	createInput := &s3.CreateMultipartUploadInput{
		Bucket:      &bucket,
		Key:         &key,
		ContentType: &ct,
	}
	if storageClass != "" {
		createInput.StorageClass = storageClass
	}

	createResp, err := client.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return fmt.Errorf("create multipart upload: %w", err)
	}
	uploadID := createResp.UploadId

	abortUpload := func() {
		_, abortErr := client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   &bucket,
			Key:      &key,
			UploadId: uploadID,
		})
		if abortErr != nil {
			slog.Debug("abort multipart upload failed", "key", key, "error", abortErr)
		}
	}

	buf := make([]byte, multipartPartSize)
	var completedParts []s3types.CompletedPart
	partNum := int32(1)

	for {
		n, readErr := io.ReadFull(f, buf)
		if n == 0 {
			break
		}
		chunk := buf[:n]
		size := int64(n)
		pn := partNum

		slog.Debug("uploading part", "key", key, "part", pn, "size", size)
		var lastPartErr error
		for attempt := 0; attempt <= len(partRetryDelays); attempt++ {
			if attempt > 0 {
				delay := partRetryDelays[attempt-1]
				select {
				case <-ctx.Done():
					abortUpload()
					return ctx.Err()
				case <-time.After(delay):
				}
				slog.Debug("retrying part upload", "key", key, "part", pn, "attempt", attempt+1)
			}
			partResp, err := client.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:        &bucket,
				Key:           &key,
				UploadId:      uploadID,
				PartNumber:    &pn,
				Body:          bytes.NewReader(chunk),
				ContentLength: &size,
			})
			if err == nil {
				completedParts = append(completedParts, s3types.CompletedPart{
					ETag:       partResp.ETag,
					PartNumber: &pn,
				})
				lastPartErr = nil
				break
			}
			lastPartErr = err
			slog.Debug("part upload failed", "key", key, "part", pn, "attempt", attempt+1, "error", err)
		}
		if lastPartErr != nil {
			abortUpload()
			return fmt.Errorf("upload part %d: %w", pn, lastPartErr)
		}
		partNum++

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			abortUpload()
			return fmt.Errorf("read part %d: %w", pn, readErr)
		}
	}

	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   &bucket,
		Key:      &key,
		UploadId: uploadID,
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		abortUpload()
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	slog.Debug("multipart upload complete", "key", key, "parts", len(completedParts))
	return nil
}
