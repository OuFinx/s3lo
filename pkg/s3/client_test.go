package s3

import (
	"context"
	"testing"
)

func TestNewClient(t *testing.T) {
	c, err := NewClient(context.Background())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestParseBucketRegionCache(t *testing.T) {
	c := &Client{
		regionCache: map[string]string{
			"my-bucket": "eu-west-1",
		},
	}

	region, ok := c.cachedRegion("my-bucket")
	if !ok {
		t.Fatal("expected cached region")
	}
	if region != "eu-west-1" {
		t.Errorf("region = %q, want %q", region, "eu-west-1")
	}

	_, ok = c.cachedRegion("other-bucket")
	if ok {
		t.Fatal("expected no cached region for other-bucket")
	}
}
