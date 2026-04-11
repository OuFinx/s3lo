package main

import (
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/pkg/image"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean up old tags and unreferenced blobs",
}

var (
	cleanConfirm   bool
	cleanTagsOnly  bool
	cleanBlobsOnly bool
	cleanConfig    string
)

var cleanRunCmd = &cobra.Command{
	Use:   "run <s3-bucket-ref>",
	Short: "Prune old tags and garbage collect unreferenced blobs",
	Long: `Removes old image tags according to lifecycle rules, then garbage collects
unreferenced blobs. Runs in dry-run mode by default — no deletions are performed.

Lifecycle rules are read from the bucket's s3lo.yaml. Use --config to override
with a local file.

Use --tags-only to only prune tags, or --blobs-only to only collect blobs.`,
	Example: `  s3lo clean run s3://my-bucket/                  # dry run
  s3lo clean run s3://my-bucket/ --confirm         # prune tags + gc blobs
  s3lo clean run s3://my-bucket/ --tags-only       # dry run, tags only
  s3lo clean run s3://my-bucket/ --blobs-only      # dry run, blobs only
  s3lo clean run s3://my-bucket/ --confirm --tags-only`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cleanTagsOnly && cleanBlobsOnly {
			return fmt.Errorf("--tags-only and --blobs-only are mutually exclusive")
		}

		dryRun := !cleanConfirm
		s3Ref := args[0]

		if !cleanBlobsOnly {
			var cfg *image.BucketConfig

			if cleanConfig != "" {
				data, err := os.ReadFile(cleanConfig)
				if err != nil {
					return fmt.Errorf("read config file: %w", err)
				}
				cfg, err = image.LoadBucketConfigFromFile(data)
				if err != nil {
					return err
				}
			} else {
				bucket, _, err := image.ParseBucketRef(s3Ref)
				if err != nil {
					return err
				}
				client, err := s3client.NewClient(cmd.Context())
				if err != nil {
					return err
				}
				cfg, err = image.GetBucketConfig(cmd.Context(), client, bucket)
				if err != nil {
					return err
				}
			}

			lcResult, err := image.ApplyLifecycle(cmd.Context(), s3Ref, cfg, dryRun)
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Printf("Tags:  %d would be deleted (out of %d evaluated)\n",
					lcResult.Deleted, lcResult.Evaluated)
			} else {
				fmt.Printf("Tags:  %d deleted (out of %d evaluated)\n",
					lcResult.Deleted, lcResult.Evaluated)
			}
		}

		if !cleanTagsOnly {
			gcResult, err := image.GC(cmd.Context(), s3Ref, dryRun)
			if err != nil {
				return err
			}

			if dryRun {
				fmt.Printf("Blobs: %d unreferenced (%.2f MB would be freed)\n",
					gcResult.Deleted, float64(gcResult.FreedBytes)/1024/1024)
			} else {
				fmt.Printf("Blobs: %d deleted (%.2f MB freed)\n",
					gcResult.Deleted, float64(gcResult.FreedBytes)/1024/1024)
			}
		}

		if dryRun {
			fmt.Println("\nRun with --confirm to apply changes.")
		}

		return nil
	},
}

func init() {
	cleanRunCmd.Flags().BoolVar(&cleanConfirm, "confirm", false, "Actually delete (default is dry-run)")
	cleanRunCmd.Flags().BoolVar(&cleanTagsOnly, "tags-only", false, "Only prune old tags, skip blob gc")
	cleanRunCmd.Flags().BoolVar(&cleanBlobsOnly, "blobs-only", false, "Only gc unreferenced blobs, skip tag pruning")
	cleanRunCmd.Flags().StringVar(&cleanConfig, "config", "", "Path to BucketConfig YAML file (optional; defaults to bucket's s3lo.yaml)")
	cleanCmd.AddCommand(cleanRunCmd)
	rootCmd.AddCommand(cleanCmd)
}
