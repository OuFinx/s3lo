package main

import (
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/v3/pkg/image"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
	"github.com/spf13/cobra"
)

var (
	cleanConfirm bool
	cleanTags    bool
	cleanBlobs   bool
	cleanConfig  string
)

var cleanCmd = &cobra.Command{
	Use:   "clean <s3-bucket-ref>",
	Short: "Prune old tags and garbage collect unreferenced blobs",
	Long: `Removes old image tags according to lifecycle rules, then garbage collects
unreferenced blobs. Runs in dry-run mode by default — no deletions are performed.

Lifecycle rules are read from the bucket's s3lo.yaml. Use --config to override
with a local file.

Use --tags to only prune tags, or --blobs to only collect blobs.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/clean/

  s3lo clean s3://my-bucket/                  # dry run
  s3lo clean s3://my-bucket/ --confirm        # prune tags + gc blobs
  s3lo clean s3://my-bucket/ --tags           # dry run, tags only
  s3lo clean s3://my-bucket/ --blobs          # dry run, blobs only
  s3lo clean s3://my-bucket/ --confirm --tags`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cleanTags && cleanBlobs {
			return fmt.Errorf("--tags and --blobs are mutually exclusive")
		}

		dryRun := !cleanConfirm
		s3Ref := args[0]
		result := cleanResult{Ref: s3Ref, DryRun: dryRun}

		if !cleanBlobs {
			cfg, err := loadCleanConfig(cmd, s3Ref)
			if err != nil {
				return err
			}

			lcResult, err := image.ApplyLifecycle(cmd.Context(), s3Ref, cfg, dryRun)
			if err != nil {
				return err
			}
			result.TagsEvaluated = lcResult.Evaluated
			result.TagsDeleted = lcResult.Deleted
		}

		if !cleanTags {
			gcResult, err := image.GC(cmd.Context(), s3Ref, dryRun)
			if err != nil {
				return err
			}
			result.BlobsDeleted = gcResult.Deleted
			result.FreedBytes = gcResult.FreedBytes
			result.SkippedRecent = gcResult.SkippedRecent
		}

		ok, err := writeOutput(outputFormat(cmd), result)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		if !cleanBlobs {
			verb := "deleted"
			if dryRun {
				verb = "would be deleted"
			}
			status("Tags:  %d %s (out of %d evaluated)\n", result.TagsDeleted, verb, result.TagsEvaluated)
		}
		if !cleanTags {
			if dryRun {
				status("Blobs: %d unreferenced (%s would be freed)\n",
					result.BlobsDeleted, formatBytes(result.FreedBytes))
			} else {
				status("Blobs: %d deleted (%s freed)\n",
					result.BlobsDeleted, formatBytes(result.FreedBytes))
			}
			// Otherwise deleting a tag and running clean straight after prints a
			// bare "0 deleted" and reads as broken, when the sweep is simply
			// waiting out the window that keeps it from racing a running push.
			if result.SkippedRecent > 0 {
				status("       %d unreferenced object(s) left for now: anything written in the last hour is\n", result.SkippedRecent)
				status("       held back so a concurrent push cannot have its blobs swept before its manifest lands.\n")
			}
		}

		if dryRun {
			status("\nRun with --confirm to apply changes.\n")
		} else if cleanTags {
			status("\nNote: orphaned blobs from deleted tags remain until GC runs.\n")
			status("      Run: s3lo clean %s --blobs --confirm\n", s3Ref)
		}

		return nil
	},
}

// cleanResult is what a clean run did, in one object, because a caller piping
// --output json wants both halves of the run rather than two separate lines.
type cleanResult struct {
	Ref           string `json:"ref" yaml:"ref"`
	DryRun        bool   `json:"dry_run" yaml:"dry_run"`
	TagsEvaluated int    `json:"tags_evaluated" yaml:"tags_evaluated"`
	TagsDeleted   int    `json:"tags_deleted" yaml:"tags_deleted"`
	BlobsDeleted  int    `json:"blobs_deleted" yaml:"blobs_deleted"`
	FreedBytes    int64  `json:"freed_bytes" yaml:"freed_bytes"`
	SkippedRecent int    `json:"skipped_recent" yaml:"skipped_recent"`
}

func loadCleanConfig(cmd *cobra.Command, s3Ref string) (*image.BucketConfig, error) {
	if cleanConfig != "" {
		data, err := os.ReadFile(cleanConfig)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		return image.LoadBucketConfigFromFile(data)
	}
	bucket, _, err := image.ParseBucketRef(s3Ref)
	if err != nil {
		return nil, err
	}
	client, err := storage.NewBackendFromRef(cmd.Context(), s3Ref)
	if err != nil {
		return nil, err
	}
	return image.GetBucketConfig(cmd.Context(), client, bucket)
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanConfirm, "confirm", false, "Actually delete (default is dry-run)")
	cleanCmd.Flags().BoolVar(&cleanTags, "tags", false, "Only prune old tags, skip blob GC (orphaned blobs remain until --blobs is run)")
	cleanCmd.Flags().BoolVar(&cleanBlobs, "blobs", false, "Only gc unreferenced blobs, skip tag pruning")
	cleanCmd.Flags().StringVar(&cleanConfig, "config", "", "Path to BucketConfig YAML file (optional; defaults to bucket's s3lo.yaml)")
	addOutputFlag(cleanCmd)
	rootCmd.AddCommand(cleanCmd)
}
