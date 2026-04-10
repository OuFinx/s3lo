package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var gcConfirm bool

var gcCmd = &cobra.Command{
	Use:   "gc <s3-bucket-ref>",
	Short: "Garbage collect unreferenced blobs from S3",
	Long: `Remove blobs in blobs/sha256/ not referenced by any manifest.

Runs in dry-run mode by default — no deletions are performed.
Use --confirm to actually delete unreferenced blobs.
Blobs uploaded within the last hour are always preserved.`,
	Example: `  s3lo gc s3://my-bucket/             # dry run (safe)
  s3lo gc s3://my-bucket/ --confirm   # delete unreferenced blobs`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun := !gcConfirm
		result, err := image.GC(cmd.Context(), args[0], dryRun)
		if err != nil {
			return err
		}
		if dryRun {
			fmt.Printf("Dry run: %d unreferenced blob(s) found (%.2f MB)\n",
				result.Deleted, float64(result.FreedBytes)/1024/1024)
			if result.Deleted > 0 {
				fmt.Println("Run with --confirm to delete them.")
			}
		} else {
			fmt.Printf("Deleted %d blob(s), %.2f MB freed\n",
				result.Deleted, float64(result.FreedBytes)/1024/1024)
		}
		return nil
	},
}

func init() {
	gcCmd.Flags().BoolVar(&gcConfirm, "confirm", false, "Actually delete unreferenced blobs (default is dry-run)")
	rootCmd.AddCommand(gcCmd)
}
