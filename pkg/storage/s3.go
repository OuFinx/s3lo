package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	baseClient  *s3.Client
	baseCfg     aws.Config
	mu          sync.RWMutex
	regionCache map[string]string
	clientCache map[string]*s3.Client
}

// NewS3Client creates an S3 client using the default AWS credentials chain.
func NewS3Client(ctx context.Context) (*Client, error) {
	return newS3Client(ctx, "")
}

// newS3Client is the internal constructor. endpoint is empty for AWS S3, non-empty for S3-compatible backends.
// Full endpoint support (UsePathStyle etc.) will be added in a later task.
func newS3Client(ctx context.Context, endpoint string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	// GetBucketLocation works against the global S3 endpoint. If the user's
	// profile has no region configured, the SDK fails with a cryptic DNS error.
	// Default the base client to us-east-1 so the initial API call always works.
	baseCfg := cfg
	if baseCfg.Region == "" {
		baseCfg.Region = "us-east-1"
	}

	return &Client{
		baseClient:  s3.NewFromConfig(baseCfg),
		baseCfg:     cfg,
		regionCache: make(map[string]string),
		clientCache: make(map[string]*s3.Client),
	}, nil
}

// ClientForBucket returns an S3 client configured for the bucket's region (public).
// Clients are cached per-region to avoid re-loading AWS config on every call.
func (c *Client) ClientForBucket(ctx context.Context, bucket string) (*s3.Client, error) {
	region, ok := c.cachedRegion(bucket)
	if !ok {
		var err error
		region, err = c.detectRegion(ctx, bucket)
		if err != nil {
			return nil, err
		}
		c.cacheRegion(bucket, region)
	}

	// Return cached client for this region if available.
	c.mu.RLock()
	if client, ok := c.clientCache[region]; ok {
		c.mu.RUnlock()
		return client, nil
	}
	c.mu.RUnlock()

	regionCfg := c.baseCfg
	regionCfg.Region = region
	client := s3.NewFromConfig(regionCfg)

	c.mu.Lock()
	c.clientCache[region] = client
	c.mu.Unlock()

	return client, nil
}

func (c *Client) detectRegion(ctx context.Context, bucket string) (string, error) {
	loc, err := c.baseClient.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: &bucket,
	})
	if err != nil {
		return "", fmt.Errorf("get bucket location for %s: %w", bucket, err)
	}

	region := string(loc.LocationConstraint)
	if region == "" {
		region = "us-east-1"
	}
	return region, nil
}

func (c *Client) cachedRegion(bucket string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.regionCache[bucket]
	return r, ok
}

func (c *Client) cacheRegion(bucket, region string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.regionCache[bucket] = region
}
