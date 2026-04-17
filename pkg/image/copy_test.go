package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

func TestParseOCIRef(t *testing.T) {
	tests := []struct {
		input        string
		wantRegistry string
		wantImage    string
		wantTag      string
		wantErr      bool
	}{
		// Docker shorthand — the common case users expect to "just work"
		{"alpine", "registry-1.docker.io", "library/alpine", "latest", false},
		{"alpine:latest", "registry-1.docker.io", "library/alpine", "latest", false},
		{"alpine:3.18", "registry-1.docker.io", "library/alpine", "3.18", false},
		{"nginx:1.25", "registry-1.docker.io", "library/nginx", "1.25", false},
		// Docker Hub user images
		{"user/myapp:v1.0", "registry-1.docker.io", "user/myapp", "v1.0", false},
		{"user/myapp", "registry-1.docker.io", "user/myapp", "latest", false},
		// Explicit docker.io
		{"docker.io/library/alpine:latest", "docker.io", "library/alpine", "latest", false},
		{"docker.io/user/myapp:v1.0", "docker.io", "user/myapp", "v1.0", false},
		// GHCR
		{"ghcr.io/owner/image:tag", "ghcr.io", "owner/image", "tag", false},
		// ECR
		{"123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0", "123456789.dkr.ecr.us-east-1.amazonaws.com", "myapp", "v1.0", false},
		// localhost
		{"localhost:5000/myapp:dev", "localhost:5000", "myapp", "dev", false},
		// Protocol prefixes are stripped
		{"https://ghcr.io/owner/image:tag", "ghcr.io", "owner/image", "tag", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			reg, img, tag, err := parseOCIRef(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if reg != tc.wantRegistry || img != tc.wantImage || tag != tc.wantTag {
				t.Errorf("parseOCIRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tc.input, reg, img, tag, tc.wantRegistry, tc.wantImage, tc.wantTag)
			}
		})
	}
}

func makeSingleArchStore(t *testing.T, parentDir, storeName, imageName, tag string) string {
	t.Helper()

	storeDir := filepath.Join(parentDir, storeName)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":6},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":5}]}`)

	writeTestFile(t, filepath.Join(storeDir, "manifests", imageName, tag, "manifest.json"), manifest)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("a", 64)), []byte("config"))
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", strings.Repeat("b", 64)), []byte("layer"))

	return "local://./" + storeName + "/" + imageName + ":" + tag
}

func TestCopy_ImmutableDestinationBlocked(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parentDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	srcRef := makeSingleArchStore(t, parentDir, "srcstore", "myapp", "v1.0")
	destRef := makeSingleArchStore(t, parentDir, "dststore", "myapp", "v1.0")

	client := storage.NewLocalClient()
	immutable := true
	cfg := &BucketConfig{
		Images: map[string]ImageConfig{
			"myapp": {Immutable: &immutable},
		},
	}
	if err := SetBucketConfig(ctx, client, filepath.Join(parentDir, "dststore"), cfg); err != nil {
		t.Fatal(err)
	}

	_, err = Copy(ctx, srcRef, destRef, CopyOptions{})
	if err == nil {
		t.Fatal("expected immutable copy error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "immutable") {
		t.Fatalf("expected immutable error, got %q", got)
	}
}

func TestCopy_ImmutableDestinationForce(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parentDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	srcRef := makeSingleArchStore(t, parentDir, "srcstore", "myapp", "v1.0")
	destRef := makeSingleArchStore(t, parentDir, "dststore", "myapp", "v1.0")

	client := storage.NewLocalClient()
	immutable := true
	cfg := &BucketConfig{
		Images: map[string]ImageConfig{
			"myapp": {Immutable: &immutable},
		},
	}
	if err := SetBucketConfig(ctx, client, filepath.Join(parentDir, "dststore"), cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Copy(ctx, srcRef, destRef, CopyOptions{Force: true})
	if err != nil {
		t.Fatalf("Copy with force: %v", err)
	}
	if result.BlobsCopied == 0 && result.BlobsSkipped == 0 {
		t.Fatalf("unexpected copy result: %+v", result)
	}
}
