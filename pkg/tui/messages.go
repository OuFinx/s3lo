package tui

import "time"

// ImageListEntry is one row in the bucket-level image list.
type ImageListEntry struct {
	Name       string
	TagCount   int
	TotalBytes int64
}

// TagEntry is one row in the image-level tag list.
type TagEntry struct {
	Name         string
	LastModified time.Time
}

// TagStats holds on-demand metadata for a single tag, loaded when the cursor lands on it.
type TagStats struct {
	TotalBytes  int64
	LayerCount  int
	Platforms   []string // ["linux/amd64", "linux/arm64"]; empty for single-arch
	Signed      bool
	CostMonthly float64
}

// BucketStats holds bucket-level statistics for the right panel.
type BucketStats struct {
	Images       int
	Tags         int
	UniqueBlobs  int
	TotalBytes   int64
	LogicalBytes int64
	CostMonthly  float64
	ECRMonthly   float64
	SavingsPct   float64
}

// CleanPreview holds a dry-run summary of what a clean would do.
type CleanPreview struct {
	TagsPruned int
	BlobsFreed int
	FreedBytes int64
}

// --- bubbletea message types ---

type imagesFetchedMsg struct {
	entries []ImageListEntry
	err     error
}

type tagsFetchedMsg struct {
	imageName string
	tags      []TagEntry
	err       error
}

type tagStatsFetchedMsg struct {
	cacheKey string // "<imageName>:<tagName>"
	stats    TagStats
	err      error
}

type bucketStatsFetchedMsg struct {
	stats BucketStats
	err   error
}

type deleteResultMsg struct {
	err error
}

type cleanPreviewFetchedMsg struct {
	preview CleanPreview
	err     error
}

type cleanResultMsg struct {
	deleted int
	freed   int64
	err     error
}

type inspectResultMsg struct {
	content string
	err     error
}

type scanResultMsg struct {
	err error
}

type scanPreparedMsg struct {
	tmpDir    string
	trivyPath string
	err       error
}

// statusClearMsg is sent 4 seconds after a status message to clear it.
type statusClearMsg struct{}

// confirmMsg is emitted by ConfirmDialog when the user confirms.
type confirmMsg struct {
	action string // "delete" | "clean"
	target string // s3Ref for delete, "" for clean
}
