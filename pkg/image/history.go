package image

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// HistoryEntry records a single push event for an image tag.
type HistoryEntry struct {
	PushedAt  time.Time `json:"pushed_at"`
	Digest    string    `json:"digest"`
	SizeBytes int64     `json:"size_bytes"`
}

// GetHistory returns the push history for an image tag (newest first).
// Returns nil, nil if no history has been recorded yet.
func GetHistory(ctx context.Context, s3Ref string) ([]HistoryEntry, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid S3 reference: %w", err)
	}
	client, err := s3client.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}
	return readHistory(ctx, client, parsed)
}

// readHistory reads history.json for the given image tag.
func readHistory(ctx context.Context, client s3client.Backend, parsed ref.Reference) ([]HistoryEntry, error) {
	key := parsed.ManifestsPrefix() + "history.json"
	data, err := client.GetObject(ctx, parsed.Bucket, key)
	if err != nil {
		if s3client.IsNotFound(err) {
			return nil, nil // no history yet
		}
		return nil, fmt.Errorf("read history: %w", err)
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse history: %w", err)
	}
	return entries, nil
}

// recordHistory prepends a new push event to the tag's history.json.
// Called from Push after a successful upload.
func recordHistory(ctx context.Context, client s3client.Backend, parsed ref.Reference, manifestData []byte, sizeBytes int64) error {
	h := sha256.Sum256(manifestData)
	entry := HistoryEntry{
		PushedAt:  time.Now().UTC().Truncate(time.Second),
		Digest:    fmt.Sprintf("sha256:%x", h),
		SizeBytes: sizeBytes,
	}

	// Read existing history, prepend new entry (keep newest first).
	entries, _ := readHistory(ctx, client, parsed) // ignore read errors; start fresh
	entries = append([]HistoryEntry{entry}, entries...)

	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	key := parsed.ManifestsPrefix() + "history.json"
	return client.PutObject(ctx, parsed.Bucket, key, data)
}
