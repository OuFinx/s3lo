package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull <s3-ref> [image-tag]",
	Short: "Pull an image from S3 into local Docker",
	Long:  "Download an OCI image from S3 and import it into the local Docker daemon.",
	Example: `  s3lo pull s3://my-bucket/myapp:v1.0
  s3lo pull s3://my-bucket/myapp:v1.0 myapp:v1.0`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageTag := ""
		if len(args) > 1 {
			imageTag = args[1]
		}
		fmt.Printf("Pulling %s...\n", args[0])
		if err := image.Pull(cmd.Context(), args[0], imageTag); err != nil {
			return err
		}
		fmt.Println("Done. Image imported into Docker.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pullCmd)
}
