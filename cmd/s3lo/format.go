package main

import (
	"fmt"
	"os"
	"time"

	"github.com/schollz/progressbar/v3"
)

// newProgressBar creates a progress bar that writes to stderr.
// If total > 0, shows a deterministic percentage bar; otherwise shows an indeterminate spinner.
// In non-TTY environments (CI, piped output) it is automatically silenced.
func newProgressBar(description string, total int64) *progressbar.ProgressBar {
	if total > 0 {
		return progressbar.NewOptions64(
			total,
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionSetDescription(description),
			progressbar.OptionShowBytes(true),
			progressbar.OptionThrottle(50*time.Millisecond),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
		)
	}
	return progressbar.NewOptions64(
		-1, // indeterminate
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription(description),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionThrottle(50*time.Millisecond),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
	)
}

// formatBytes returns a human-readable byte size string.
func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/GB)
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/KB)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// formatCost returns a human-readable monthly cost string.
// For amounts between 0 (exclusive) and 0.01, shows "< $0.01" to avoid misleading "$0.00".
func formatCost(amount float64) string {
	if amount > 0 && amount < 0.01 {
		return "< $0.01"
	}
	return fmt.Sprintf("$%.2f", amount)
}
