package storage

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
)

func TestAzureClientImplementsBackend(t *testing.T) {
	var _ Backend = (*AzureClient)(nil)
}

func TestToAzureUploadOptions(t *testing.T) {
	t.Run("IntelligentTiering maps to Cool", func(t *testing.T) {
		opts := toAzureUploadOptions(StorageClassIntelligentTiering)
		if opts == nil {
			t.Fatal("expected non-nil options")
		}
		if opts.AccessTier == nil {
			t.Fatal("expected non-nil AccessTier")
		}
		if *opts.AccessTier != blob.AccessTierCool {
			t.Errorf("AccessTier = %v, want Cool", *opts.AccessTier)
		}
	})

	t.Run("Standard maps to Hot", func(t *testing.T) {
		opts := toAzureUploadOptions(StorageClassStandard)
		if opts == nil {
			t.Fatal("expected non-nil options")
		}
		if opts.AccessTier == nil {
			t.Fatal("expected non-nil AccessTier")
		}
		if *opts.AccessTier != blob.AccessTierHot {
			t.Errorf("AccessTier = %v, want Hot", *opts.AccessTier)
		}
	})

	t.Run("unknown returns nil", func(t *testing.T) {
		opts := toAzureUploadOptions(StorageClass("UNKNOWN"))
		if opts != nil {
			t.Errorf("expected nil options for unknown class, got %v", opts)
		}
	})

	t.Run("empty returns nil", func(t *testing.T) {
		opts := toAzureUploadOptions(StorageClass(""))
		if opts != nil {
			t.Errorf("expected nil options for empty class, got %v", opts)
		}
	})
}

func TestAzureNotFoundError(t *testing.T) {
	err := &azureNotFoundError{container: "my-container", blob: "my-blob"}
	want := "object not found: az://my-container/my-blob"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound should return true for azureNotFoundError")
	}
}

func TestIsNotFound_NilError(t *testing.T) {
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) should return false")
	}
}
