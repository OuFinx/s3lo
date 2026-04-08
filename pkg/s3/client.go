package s3

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	baseClient  *s3.Client
	mu          sync.RWMutex
	regionCache map[string]string
}

func NewClient() (*Client, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &Client{
		baseClient:  s3.NewFromConfig(cfg),
		regionCache: make(map[string]string),
	}, nil
}

// ClientForBucket returns an S3 client configured for the bucket's region (public).
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

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config for region %s: %w", region, err)
	}

	return s3.NewFromConfig(cfg), nil
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
