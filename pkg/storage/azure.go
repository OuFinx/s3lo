package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
)

// Compile-time assertion that AzureClient implements Backend.
var _ Backend = (*AzureClient)(nil)

// AzureClient implements Backend using Azure Blob Storage.
type AzureClient struct {
	client      *azblob.Client
	accountName string
}

// newAzureBackend creates an AzureClient and returns it as a Backend.
func newAzureBackend(ctx context.Context) (Backend, error) {
	return newAzureClient(ctx)
}

// newAzureClient creates a new Azure Blob client using DefaultAzureCredential.
// The storage account name must be set in the AZURE_STORAGE_ACCOUNT environment variable.
func newAzureClient(ctx context.Context) (*AzureClient, error) {
	accountName := os.Getenv("AZURE_STORAGE_ACCOUNT")
	if accountName == "" {
		return nil, fmt.Errorf("AZURE_STORAGE_ACCOUNT environment variable not set")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}
	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", accountName)
	client, err := azblob.NewClient(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure blob client: %w", err)
	}
	return &AzureClient{client: client, accountName: accountName}, nil
}

func (c *AzureClient) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	resp, err := c.client.DownloadStream(ctx, bucket, key, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return nil, &azureNotFoundError{container: bucket, blob: key}
		}
		return nil, fmt.Errorf("azure get %s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azure read %s/%s: %w", bucket, key, err)
	}
	return data, nil
}

func (c *AzureClient) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	if _, err := c.client.UploadBuffer(ctx, bucket, key, data, nil); err != nil {
		return fmt.Errorf("azure put %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *AzureClient) HeadObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	blobClient := c.client.ServiceClient().NewContainerClient(bucket).NewBlobClient(key)
	_, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("azure head %s/%s: %w", bucket, key, err)
	}
	return true, nil
}

func (c *AzureClient) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	pager := c.client.NewListBlobsFlatPager(bucket, &azblob.ListBlobsFlatOptions{Prefix: &prefix})
	var keys []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azure list %s/%s: %w", bucket, prefix, err)
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name != nil {
				keys = append(keys, *item.Name)
			}
		}
	}
	return keys, nil
}

func (c *AzureClient) ListObjectsWithMeta(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error) {
	pager := c.client.NewListBlobsFlatPager(bucket, &azblob.ListBlobsFlatOptions{Prefix: &prefix})
	var objects []ObjectMeta
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azure list-meta %s/%s: %w", bucket, prefix, err)
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name == nil {
				continue
			}
			meta := ObjectMeta{Key: *item.Name}
			if item.Properties != nil {
				if item.Properties.ContentLength != nil {
					meta.Size = *item.Properties.ContentLength
				}
				if item.Properties.LastModified != nil {
					meta.LastModified = *item.Properties.LastModified
				}
			}
			objects = append(objects, meta)
		}
	}
	return objects, nil
}

func (c *AzureClient) DeleteObjects(ctx context.Context, bucket string, keys []string) error {
	var errs []error
	for _, key := range keys {
		if _, err := c.client.DeleteBlob(ctx, bucket, key, nil); err != nil {
			if !isAzureNotFound(err) {
				errs = append(errs, fmt.Errorf("azure delete %s/%s: %w", bucket, key, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (c *AzureClient) UploadFile(ctx context.Context, localPath, bucket, key string, sc StorageClass) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()
	opts := toAzureUploadOptions(sc)
	if _, err := c.client.UploadFile(ctx, bucket, key, f, opts); err != nil {
		return fmt.Errorf("azure upload %s → %s/%s: %w", localPath, bucket, key, err)
	}
	return nil
}

// toAzureUploadOptions maps our StorageClass to Azure upload options with an access tier.
// INTELLIGENT_TIERING → Cool (cheaper storage, like S3 IT's infrequent tier).
// STANDARD → Hot (default, frequent access).
func toAzureUploadOptions(sc StorageClass) *azblob.UploadFileOptions {
	switch sc {
	case StorageClassIntelligentTiering:
		tier := blob.AccessTierCool
		return &azblob.UploadFileOptions{AccessTier: &tier}
	case StorageClassStandard:
		tier := blob.AccessTierHot
		return &azblob.UploadFileOptions{AccessTier: &tier}
	default:
		return nil
	}
}

func (c *AzureClient) DownloadObjectToFile(ctx context.Context, bucket, key, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(localPath), ".s3lo-download-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	defer func() {
		f.Close()
		os.Remove(tmpPath)
	}()

	if _, err := c.client.DownloadFile(ctx, bucket, key, f, nil); err != nil {
		if isAzureNotFound(err) {
			return &azureNotFoundError{container: bucket, blob: key}
		}
		return fmt.Errorf("azure download %s/%s → %s: %w", bucket, key, localPath, err)
	}
	f.Close()
	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("rename %s → %s: %w", tmpPath, localPath, err)
	}
	return nil
}

func (c *AzureClient) DownloadDirectory(ctx context.Context, bucket, prefix, destDir string) error {
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

func (c *AzureClient) CopyObject(ctx context.Context, bucket, srcKey, destKey string) error {
	srcBlobClient := c.client.ServiceClient().NewContainerClient(bucket).NewBlobClient(srcKey)
	srcURL := srcBlobClient.URL()
	destBlobClient := c.client.ServiceClient().NewContainerClient(bucket).NewBlobClient(destKey)
	resp, err := destBlobClient.StartCopyFromURL(ctx, srcURL, nil)
	if err != nil {
		return fmt.Errorf("azure copy %s/%s → %s: %w", bucket, srcKey, destKey, err)
	}
	if resp.CopyStatus != nil && *resp.CopyStatus == "pending" {
		backoff := 500 * time.Millisecond
		const maxBackoff = 10 * time.Second
		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("azure copy %s/%s → %s: %w", bucket, srcKey, destKey, ctx.Err())
			case <-time.After(backoff):
			}
			props, err := destBlobClient.GetProperties(ctx, nil)
			if err != nil {
				return fmt.Errorf("azure copy poll %s/%s → %s: %w", bucket, srcKey, destKey, err)
			}
			status := ""
			if props.CopyStatus != nil {
				status = string(*props.CopyStatus)
			}
			switch status {
			case "pending":
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				continue
			case "failed":
				return fmt.Errorf("azure copy %s/%s → %s: server-side copy failed", bucket, srcKey, destKey)
			default:
				return nil
			}
		}
	}
	return nil
}

// isAzureNotFound returns true if err is an Azure 404 response error.
func isAzureNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == 404
	}
	return false
}

// azureNotFoundError signals that an object doesn't exist in Azure Blob Storage.
type azureNotFoundError struct{ container, blob string }

func (e *azureNotFoundError) Error() string {
	return fmt.Sprintf("object not found: az://%s/%s", e.container, e.blob)
}
