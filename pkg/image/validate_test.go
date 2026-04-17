package image

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// makeLocalFixture creates a minimal image fixture and returns the local:// ref.
// Caller must os.Chdir(parentDir) before calling Validate with the returned ref.
func makeLocalFixture(t *testing.T, parentDir, storeName, imageName, tag string) string {
	t.Helper()
	manifestDir := filepath.Join(parentDir, storeName, "manifests", imageName, tag)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","size":0},"layers":[]}`)
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return "local://./" + storeName + "/" + imageName + ":" + tag
}

func TestValidateNoPoliciesPasses(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	oldCwd, _ := os.Getwd()
	_ = os.Chdir(parentDir)
	defer os.Chdir(oldCwd)

	localRef := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")
	storeDir := filepath.Join(parentDir, "mystore")
	client := storage.NewLocalClient()

	cfg := &BucketConfig{}
	if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Validate(ctx, localRef, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllPassed {
		t.Errorf("expected AllPassed=true with no policies")
	}
	if len(result.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(result.Results))
	}
}

func TestValidateAgePolicy_fail(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	oldCwd, _ := os.Getwd()
	_ = os.Chdir(parentDir)
	defer os.Chdir(oldCwd)

	localRef := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")
	storeDir := filepath.Join(parentDir, "mystore")
	client := storage.NewLocalClient()

	oldTime := time.Now().AddDate(0, 0, -200)
	entries := []HistoryEntry{{PushedAt: oldTime, Digest: "sha256:abc", SizeBytes: 1000}}
	data, _ := json.Marshal(entries)
	_ = client.PutObject(ctx, storeDir, "manifests/myapp/v1.0/history.json", data)

	cfg := &BucketConfig{
		Policies: []PolicyRule{{Name: "max-age", Check: PolicyCheckAge, MaxDays: 90}},
	}
	if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Validate(ctx, localRef, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.AllPassed {
		t.Error("expected AllPassed=false for 200-day-old image with 90-day limit")
	}
	if len(result.Results) != 1 || result.Results[0].Passed {
		t.Errorf("expected 1 failing result, got %+v", result.Results)
	}
}

func TestValidateSignedPolicy_noSignature(t *testing.T) {
	ctx := context.Background()
	_, bucketRelDir, _, pubFile := setupSignedStore(t)
	localRef := "local://./mystore/myapp:v1.0"
	client := storage.NewLocalClient()

	cfg := &BucketConfig{
		Policies: []PolicyRule{{Name: "require-signature", Check: PolicyCheckSigned, KeyRef: pubFile}},
	}
	if err := SetBucketConfig(ctx, client, bucketRelDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Validate(ctx, localRef, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.AllPassed {
		t.Error("expected AllPassed=false when no signature present")
	}
	if result.Results[0].Message == "" {
		t.Error("expected non-empty failure message")
	}
}

func TestValidateSignedPolicy_validSignature(t *testing.T) {
	ctx := context.Background()
	_, bucketRelDir, keyFile, pubFile := setupSignedStore(t)
	localRef := "local://./mystore/myapp:v1.0"
	client := storage.NewLocalClient()

	if _, err := Sign(ctx, localRef, keyFile); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	cfg := &BucketConfig{
		Policies: []PolicyRule{{Name: "require-signature", Check: PolicyCheckSigned, KeyRef: pubFile}},
	}
	if err := SetBucketConfig(ctx, client, bucketRelDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Validate(ctx, localRef, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllPassed {
		t.Fatalf("expected signed policy to pass, got %+v", result.Results)
	}
}

func TestValidateSizePolicy_pass(t *testing.T) {
	ctx := context.Background()
	parentDir := t.TempDir()
	oldCwd, _ := os.Getwd()
	_ = os.Chdir(parentDir)
	defer os.Chdir(oldCwd)

	localRef := makeLocalFixture(t, parentDir, "mystore", "myapp", "v1.0")
	storeDir := filepath.Join(parentDir, "mystore")
	client := storage.NewLocalClient()

	cfg := &BucketConfig{
		Policies: []PolicyRule{{Name: "max-size", Check: PolicyCheckSize, MaxBytes: 1 << 30}},
	}
	if err := SetBucketConfig(ctx, client, storeDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := Validate(ctx, localRef, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AllPassed {
		t.Errorf("expected AllPassed=true, got failure: %+v", result.Results)
	}
}
