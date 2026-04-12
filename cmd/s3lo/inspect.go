package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <s3-ref>",
	Short: "Inspect an image on S3",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/inspect/

  s3lo inspect s3://my-bucket/myapp:v1.0
  s3lo inspect s3://my-bucket/myapp:v1.0 --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		outputFmt, _ := cmd.Flags().GetString("output")
		info, err := image.Inspect(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		ok, err := writeOutput(outputFmt, info)
		if err != nil {
			return err
		}
		if !ok {
			printInspect(info)
		}
		return nil
	},
}

func printInspect(info *image.ImageInfo) {
	fmt.Printf("Reference: %s\n", info.Reference)
	if info.IsIndex {
		fmt.Printf("Type:      multi-arch image index (%d platform(s))\n\n", len(info.Platforms))
		for _, p := range info.Platforms {
			digestStr := p.Digest
			if len(digestStr) > 19 {
				digestStr = digestStr[:19]
			}
			fmt.Printf("  Platform: %s\n", p.Platform)
			fmt.Printf("  Digest:   %s...\n", digestStr)
			fmt.Printf("  Layers:   %d\n", len(p.Layers))
			fmt.Printf("  Size:     %.2f MB\n", float64(p.TotalSize)/1024/1024)
			for i, layer := range p.Layers {
				ld := layer.Digest
				if len(ld) > 19 {
					ld = ld[:19]
				}
				fmt.Printf("    [%d] %s... (%.2f MB)\n", i+1, ld, float64(layer.Size)/1024/1024)
			}
			fmt.Println()
		}
	} else {
		fmt.Printf("Type:      single-arch image\n")
		fmt.Printf("Layers:    %d\n", len(info.Layers))
		fmt.Printf("Total:     %.2f MB\n\n", float64(info.TotalSize)/1024/1024)
		for i, layer := range info.Layers {
			digestStr := layer.Digest
			if len(digestStr) > 19 {
				digestStr = digestStr[:19]
			}
			fmt.Printf("  [%d] %s... (%.2f MB)\n", i+1, digestStr, float64(layer.Size)/1024/1024)
		}
	}
}

func init() {
	inspectCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	rootCmd.AddCommand(inspectCmd)
}
