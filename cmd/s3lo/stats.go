package main

import (
	"fmt"
	"strings"

	"github.com/OuFinx/s3lo/v2/pkg/image"
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats <s3-bucket-ref>",
	Short: "Show storage usage and deduplication savings",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/stats/

  s3lo bucket stats s3://my-bucket/
  s3lo bucket stats s3://my-bucket/ --layers
  s3lo bucket stats s3://my-bucket/ --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")

		if layers, _ := cmd.Flags().GetBool("layers"); layers {
			sharing, err := image.LayerSharing(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ok, err := writeOutput(outputFmt, sharing)
			if err != nil {
				return err
			}
			if !ok {
				printLayerSharing(args[0], sharing)
			}
			return nil
		}

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
	if r.Chunks > 0 {
		// On a chunked bucket most bytes live in chunks, so reporting only the
		// blob count next to the total size would misattribute the storage.
		fmt.Printf("Storage:      %s across %d blobs and %d chunks (%s chunked)\n",
			formatBytes(r.BlobBytes), r.UniqueBlobs, r.Chunks, formatBytes(r.ChunkBytes))
	} else {
		fmt.Printf("Storage:      %s across %d unique blobs\n", formatBytes(r.BlobBytes), r.UniqueBlobs)
	}

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
		fmt.Printf("  %-26s %s/month\n", "S3 (current):", formatCost(c.S3Monthly))
		if savings > 0 {
			fmt.Printf("  %-26s %s/month\n", "S3 (no dedup):", formatCost(c.S3NoDedupMonthly))
		}
		fmt.Printf("  %-26s %s/month\n", "ECR equivalent:", formatCost(c.ECRMonthly))
		if c.SavingsVsECR > 0 {
			fmt.Printf("  %-26s %s/month (%.0f%% cheaper)\n", "Savings vs ECR:", formatCost(c.SavingsVsECR), c.SavingsPct)
		}
	}
}

// printLayerSharing renders one row per unique layer, most-shared first, with
// the tags that reference it.
func printLayerSharing(bucketRef string, r *image.LayerSharingResult) {
	fmt.Printf("Bucket: %s\n\n", bucketRef)
	if len(r.Layers) == 0 {
		fmt.Println("No layers found.")
		return
	}

	fmt.Printf("%-22s %-10s %-5s %s\n", "LAYER", "SIZE", "TAGS", "SHARED BY")
	for _, l := range r.Layers {
		digest := l.Digest
		if len(digest) > 12 {
			digest = digest[:12]
		}
		fmt.Printf("%-22s %-10s %-5d %s\n",
			"sha256:"+digest, formatBytes(l.Size), len(l.Tags), strings.Join(l.Tags, ", "))
	}

	fmt.Printf("\n%d unique layers across %d tags · %s stored", len(r.Layers), len(r.Tags), formatBytes(r.StoredBytes))
	if r.LogicalBytes > r.StoredBytes {
		fmt.Printf(" · %s logical · %.0f%% saved by sharing", formatBytes(r.LogicalBytes), r.DedupPercent())
	}
	fmt.Println()
}

func init() {
	statsCmd.Flags().Bool("layers", false, "List unique layers and the tags that share them")
	statsCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	bucketCmd.AddCommand(statsCmd)
}
