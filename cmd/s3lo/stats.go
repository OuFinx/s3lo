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

  s3lo stats s3://my-bucket/
  s3lo stats s3://my-bucket/ --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")
		result, err := image.Stats(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		ok, err := writeOutput(outputFmt, result)
		if err != nil {
			return err
		}
		if !ok {
			printStats(args[0], result)
		}
		return nil
	},
}

func printStats(bucketRef string, r *image.StatsResult) {
	fmt.Printf("Bucket: %s\n\n", bucketRef)
	fmt.Printf("Images:       %d\n", r.Images)
	fmt.Printf("Tags:         %d\n", r.Tags)
	fmt.Printf("Storage:      %s across %d unique blobs\n", formatBytes(r.BlobBytes), r.UniqueBlobs)

	savings := r.DedupSavings()
	if savings > 0 {
		fmt.Printf("Dedup savings: %s (%.1f%% — without dedup: %s)\n",
			formatBytes(savings), r.DedupPercent(), formatBytes(r.LogicalBytes))
	}

	if len(r.StorageByClass) > 1 {
		fmt.Println("\nStorage class breakdown:")
		for class, bytes := range r.StorageByClass {
			if r.BlobBytes > 0 {
				pct := float64(bytes) / float64(r.BlobBytes) * 100
				fmt.Printf("  %-30s %s (%.0f%%)\n", class+":", formatBytes(bytes), pct)
			}
		}
	}

	c := r.Cost
	if r.BlobBytes > 0 {
		fmt.Println("\nEstimated monthly cost:")
		fmt.Printf("  %-26s $%.2f/month\n", "S3 (current):", c.S3Monthly)
		if savings > 0 {
			fmt.Printf("  %-26s $%.2f/month\n", "S3 (no dedup):", c.S3NoDedupMonthly)
		}
		fmt.Printf("  %-26s $%.2f/month\n", "ECR equivalent:", c.ECRMonthly)
		if c.SavingsVsECR > 0 {
			fmt.Printf("  %-26s $%.2f/month (%.0f%% cheaper)\n", "Savings vs ECR:", c.SavingsVsECR, c.SavingsPct)
		}
	}
}

func init() {
	statsCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	rootCmd.AddCommand(statsCmd)
}
