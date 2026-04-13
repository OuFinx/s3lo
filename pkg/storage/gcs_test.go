package storage

import "testing"

// TestGCSClientImplementsBackend is a compile-time check that GCSClient satisfies Backend.
func TestGCSClientImplementsBackend(t *testing.T) {
	var _ Backend = (*GCSClient)(nil)
}
