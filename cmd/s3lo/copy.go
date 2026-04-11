package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var copyCmd = &cobra.Command{
	Use:   "copy <src> <s3-dest>",
	Short: "Copy an image to S3 without pulling to local Docker",
	Long: `Copy an image from S3 or an OCI registry directly to an S3 destination.

Sources:
  s3://bucket/image:tag                                         S3-to-S3 copy (server-side within same bucket)
  123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0         ECR to S3 (auto-authenticates via AWS credentials)
  docker.io/library/nginx:latest                                Docker Hub to S3
  registry.example.com/myapp:v1.0                              Any OCI registry to S3`,
	Example: `  # Copy between S3 buckets
  s3lo copy s3://source-bucket/myapp:v1.0 s3://dest-bucket/myapp:v1.0

  # Copy from ECR to S3
  s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0

  # Copy from Docker Hub to S3
  s3lo copy docker.io/library/nginx:latest s3://my-bucket/nginx:latest`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, dest := args[0], args[1]
		fmt.Printf("Copying %s to %s\n", src, dest)
		result, err := image.Copy(cmd.Context(), src, dest)
		if err != nil {
			return err
		}
		fmt.Printf("Done. %d blob(s) copied, %d skipped (already exist).\n",
			result.BlobsCopied, result.BlobsSkipped)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(copyCmd)
}
