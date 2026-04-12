package main

import (
	"fmt"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/schollz/progressbar/v3"
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
  registry.example.com/myapp:v1.0                              Any OCI registry to S3

For multi-arch images, all platforms are copied by default. Use --platform to copy a specific platform only.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/copy/

  # Copy between S3 buckets (all platforms)
  s3lo copy s3://source-bucket/myapp:v1.0 s3://dest-bucket/myapp:v1.0

  # Copy from Docker Hub (all platforms)
  s3lo copy docker.io/library/alpine:latest s3://my-bucket/alpine:latest

  # Copy a specific platform only
  s3lo copy docker.io/library/alpine:latest s3://my-bucket/alpine:latest --platform linux/amd64

  # Copy from ECR to S3
  s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src, dest := args[0], args[1]
		if strings.HasPrefix(src, "s3://") || strings.HasPrefix(src, "local://") {
			if err := requireTag(src); err != nil {
				return err
			}
		}
		if err := requireTag(dest); err != nil {
			return err
		}
		platform, _ := cmd.Flags().GetString("platform")
		fmt.Printf("Copying %s to %s\n", src, dest)
		var bar *progressbar.ProgressBar
		opts := image.CopyOptions{
			Platform: platform,
			OnStart: func(total int64) {
				bar = newProgressBar("  copying", total)
			},
			OnBlob: func(_ string, _ string, size int64, _ bool) {
				if bar != nil {
					bar.Add64(size)
				}
			},
		}
		result, err := image.Copy(cmd.Context(), src, dest, opts)
		if bar != nil {
			bar.Finish()
		}
		if err != nil {
			return err
		}
		if result.Platforms > 1 {
			fmt.Printf("Done. %d platform(s) copied, %d blob(s) copied, %d skipped (already exist).\n",
				result.Platforms, result.BlobsCopied, result.BlobsSkipped)
		} else {
			fmt.Printf("Done. %d blob(s) copied, %d skipped (already exist).\n",
				result.BlobsCopied, result.BlobsSkipped)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(copyCmd)
	copyCmd.Flags().String("platform", "", `Copy a specific platform only (e.g. "linux/amd64"). Default: copy all platforms.`)
}
