package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:     "stats <s3-bucket-ref>",
	Short:   "Show storage usage and deduplication savings",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/stats/

  s3lo stats s3://my-bucket/`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := image.Stats(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		printStats(args[0], result)
		return nil
	},
}

func printStats(bucketRef string, r *image.StatsResult) {
	fmt.Printf("Bucket: %s\n\n", bucketRef)
	fmt.Printf("Images:       %d\n", r.Images)
	fmt.Printf("Tags:         %d\n", r.Tags)
	fmt.Printf("Unique blobs: %d\n", r.UniqueBlobs)
	fmt.Printf("Total size:   %s\n", formatBytes(r.BlobBytes))

	savings := r.DedupSavings()
	if savings > 0 {
		fmt.Printf("\nDedup savings: %s (%.0f%% saved)\n", formatBytes(savings), r.DedupPercent())
	}

	if len(r.StorageByClass) > 0 {
		fmt.Println("\nStorage class breakdown:")
		for class, bytes := range r.StorageByClass {
			if r.BlobBytes > 0 {
				pct := float64(bytes) / float64(r.BlobBytes) * 100
				fmt.Printf("  %-30s %s (%.0f%%)\n", class+":", formatBytes(bytes), pct)
			}
		}
	}

	if r.BlobBytes > 0 {
		gb := float64(r.BlobBytes) / 1024 / 1024 / 1024
		s3Cost := gb * 0.023
		ecrCost := gb * 0.10
		fmt.Printf("\nEstimated monthly cost: $%.2f\n", s3Cost)
		if s3Cost > 0 {
			fmt.Printf("vs ECR equivalent:      $%.2f (%.1fx cheaper)\n", ecrCost, ecrCost/s3Cost)
		}
	}
}

func init() {
	rootCmd.AddCommand(statsCmd)
}
