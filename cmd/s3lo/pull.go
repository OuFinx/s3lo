package main

import (
	"fmt"

	"github.com/finx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull <s3-ref> [dest-dir]",
	Short: "Pull an image from S3",
	Example: `  s3lo pull s3://my-bucket/myapp:v1.0
  s3lo pull s3://my-bucket/myapp:v1.0 ./output`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		destDir := "."
		if len(args) > 1 {
			destDir = args[1]
		}
		fmt.Printf("Pulling %s...\n", args[0])
		if err := image.Pull(cmd.Context(), args[0], destDir); err != nil {
			return err
		}
		fmt.Printf("Done. Image saved to %s\n", destDir)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pullCmd)
}
