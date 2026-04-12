package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull <s3-ref> [image-tag]",
	Short: "Pull an image from S3 into local Docker",
	Long:  "Download an OCI image from S3 and import it into the local Docker daemon.",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/pull/

  s3lo pull s3://my-bucket/myapp:v1.0
  s3lo pull s3://my-bucket/myapp:v1.0 myapp:v1.0`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		platform, _ := cmd.Flags().GetString("platform")
		imageTag := ""
		if len(args) > 1 {
			imageTag = args[1]
		}
		fmt.Printf("Pulling %s\n", args[0])
		var bar *progressbar.ProgressBar
		opts := image.PullOptions{
			Platform: platform,
			OnStart: func(total int64) {
				bar = newProgressBar("  downloading", total)
			},
			OnBlob: func(_ string, size int64) {
				if bar != nil {
					bar.Add64(size)
				}
			},
		}
		err := image.Pull(cmd.Context(), args[0], imageTag, opts)
		if bar != nil {
			bar.Finish()
		}
		if err != nil {
			return err
		}
		fmt.Println("Done. Image imported into Docker.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pullCmd)
	pullCmd.Flags().String("platform", "", `Platform to pull from a multi-arch image (e.g. "linux/amd64"). Default: auto-detect host platform.`)
}
