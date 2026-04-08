package main

import (
	"fmt"

	"github.com/finx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <s3-ref>",
	Short: "Inspect an image on S3",
	Example: `  s3lo inspect s3://my-bucket/myapp:v1.0`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := image.Inspect(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Reference: %s\n", info.Reference)
		fmt.Printf("Layers:    %d\n", len(info.Layers))
		fmt.Printf("Total:     %.2f MB\n\n", float64(info.TotalSize)/1024/1024)
		for i, layer := range info.Layers {
			digestStr := layer.Digest
			if len(digestStr) > 19 {
				digestStr = digestStr[:19]
			}
			fmt.Printf("  [%d] %s (%.2f MB)\n", i+1, digestStr, float64(layer.Size)/1024/1024)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}
