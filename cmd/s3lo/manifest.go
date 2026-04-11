package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Manage multi-arch image manifests",
}

var manifestCreateCmd = &cobra.Command{
	Use:   "create <s3-dest> <s3-src> [s3-src...]",
	Short: "Create a multi-arch image index from single-arch tags",
	Long: `Build an OCI Image Index (multi-arch manifest) from existing single-arch tags stored in S3.

All source tags must reside in the same bucket as the destination tag.
The platform (os/arch) is read from each source image's config blob.`,
	Example: `  # Create a multi-arch tag from two single-arch tags
  s3lo manifest create s3://my-bucket/myapp:latest \
    s3://my-bucket/myapp:latest-amd64 \
    s3://my-bucket/myapp:latest-arm64`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dest := args[0]
		srcs := args[1:]
		fmt.Printf("Creating multi-arch manifest for %s from %d source(s)...\n", dest, len(srcs))
		result, err := image.ManifestCreate(cmd.Context(), dest, srcs)
		if err != nil {
			return err
		}
		fmt.Printf("Done. Image index written with %d platform(s).\n", result.Platforms)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(manifestCmd)
	manifestCmd.AddCommand(manifestCreateCmd)
}
