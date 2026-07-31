package main

import (
	"strings"

	"github.com/OuFinx/s3lo/v2/pkg/chunkstore"
	"github.com/OuFinx/s3lo/v2/pkg/image"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var (
	pushForce    bool
	pushPlatform string
)

var pushCmd = &cobra.Command{
	Use:   "push <local-image> <s3-ref>",
	Short: "Push a local Docker image to S3, GCS, Azure Blob, or local storage",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/push/

  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0 --platform linux/arm64`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[1]); err != nil {
			return err
		}
		status("Pushing %s to %s\n", args[0], args[1])
		var bar *progressbar.ProgressBar
		var transferred chunkstore.Stats
		opts := image.PushOptions{
			OnTransfer: func(s chunkstore.Stats) { transferred = s },
			Force:      pushForce,
			Platform:   pushPlatform,
			OnPlatformsDropped: func(pushed string, dropped []string) {
				status("Note: this image holds %s locally. Pushing %s only.\n"+
					"      Push another with --platform, or use s3lo copy to publish every platform as an index.\n",
					strings.Join(append([]string{pushed}, dropped...), ", "), pushed)
			},
			OnStart: func(total int64) {
				bar = newProgressBar("  uploading", total)
			},
			OnBlob: func(_ string, size int64, _ bool) {
				if bar != nil {
					bar.Add64(size)
				}
			},
		}
		result, err := image.Push(cmd.Context(), args[0], args[1], opts)
		if bar != nil {
			bar.Finish()
		}
		if err != nil {
			return err
		}
		ok, err := writeOutput(outputFormat(cmd), result)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if transferred.Bytes > 0 && transferred.Chunks > 0 {
			status("Chunked: uploaded %.1f MB of %.1f MB (%.1f%% deduplicated, %d/%d chunks)\n",
				float64(transferred.BytesUploaded)/(1<<20), float64(transferred.Bytes)/(1<<20),
				transferred.Deduplicated()*100, transferred.ChunksUploaded, transferred.Chunks)
		}
		status("Done.\n")
		return nil
	},
}

func init() {
	pushCmd.Flags().BoolVar(&pushForce, "force", false, "Overwrite existing tag even if bucket is immutable")
	pushCmd.Flags().StringVar(&pushPlatform, "platform", "", "Platform to push when the local image holds several (e.g. linux/arm64)")
	addOutputFlag(pushCmd)
	rootCmd.AddCommand(pushCmd)
}
