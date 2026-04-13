package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	baseClient  *s3.Client
	baseCfg     aws.Config
	endpoint    string // non-empty for S3-compatible backends; skips region detection
	mu          sync.RWMutex
	regionCache map[string]string
	clientCache map[string]*s3.Client
}

// NewS3Client creates an S3 client using the default AWS credentials chain.
func NewS3Client(ctx context.Context) (*Client, error) {
	return newS3Client(ctx, "")
}

// newS3Client is the internal constructor. endpoint is empty for AWS S3, non-empty for S3-compatible backends.
// When endpoint is set, path-style addressing is enabled and region detection is skipped (us-east-1 is used).
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

	// When using an S3-compatible endpoint (MinIO, R2, Ceph, etc.), force us-east-1
	// and skip region detection entirely — the custom server handles routing itself.
	if endpoint != "" {
		baseCfg.Region = "us-east-1"
		cfg.Region = "us-east-1"
	}

	baseClientOpts := func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	}

	// For S3-compatible backends, bypass region detection by using a pre-wired
	// clientForBucket that always returns the same endpoint-aware client.
	if endpoint != "" {
		endpointClient := s3.NewFromConfig(baseCfg, baseClientOpts)
		c := &Client{
			baseClient:  endpointClient,
			baseCfg:     baseCfg,
			endpoint:    endpoint,
			regionCache: make(map[string]string),
			clientCache: map[string]*s3.Client{"us-east-1": endpointClient},
		}
		return c, nil
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
// For S3-compatible backends (endpoint set), region detection is skipped and the
// pre-configured endpoint client is returned directly.
func (c *Client) ClientForBucket(ctx context.Context, bucket string) (*s3.Client, error) {
	if c.endpoint != "" {
		c.mu.RLock()
		client := c.clientCache["us-east-1"]
		c.mu.RUnlock()
		return client, nil
	}

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

// PresignGetObject returns a presigned GET URL for the given object valid for ttl.
// This satisfies serve.Presigner without importing pkg/serve.
func (c *Client) PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	s3Client, err := c.ClientForBucket(ctx, bucket)
	if err != nil {
		return "", err
	}
	presignClient := s3.NewPresignClient(s3Client)
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("presign object: %w", err)
	}
	return req.URL, nil
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
