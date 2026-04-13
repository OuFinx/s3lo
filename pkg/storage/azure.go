package storage

import (
	"context"
	"fmt"
)

// AzureClient implements Backend using Azure Blob Storage.
// Full implementation added in a later task.
type AzureClient struct{}

func newAzureBackend(ctx context.Context) (Backend, error) {
	return nil, fmt.Errorf("Azure backend not yet implemented")
}

type azureNotFoundError struct{ container, blob string }

func (e *azureNotFoundError) Error() string {
	return fmt.Sprintf("object not found: az://%s/%s", e.container, e.blob)
}
