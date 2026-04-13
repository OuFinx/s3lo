package storage

import "testing"

// TestAzureClientImplementsBackend is a compile-time check that AzureClient satisfies Backend.
func TestAzureClientImplementsBackend(t *testing.T) {
	var _ Backend = (*AzureClient)(nil)
}
