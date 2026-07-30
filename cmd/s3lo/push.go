package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/chunkstore"
	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var pushForce bool

var pushCmd = &cobra.Command{
	Use:   "push <local-image> <s3-ref>",
	Short: "Push a local Docker image to S3",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/push/

  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[1]); err != nil {
			return err
		}
		fmt.Printf("Pushing %s to %s\n", args[0], args[1])
		var bar *progressbar.ProgressBar
		var transferred chunkstore.Stats
		opts := image.PushOptions{
			OnTransfer: func(s chunkstore.Stats) { transferred = s },
			Force:      pushForce,
			OnStart: func(total int64) {
				bar = newProgressBar("  uploading", total)
			},
			OnBlob: func(_ string, size int64, _ bool) {
				if bar != nil {
					bar.Add64(size)
				}
			},
		}
		err := image.Push(cmd.Context(), args[0], args[1], opts)
		if bar != nil {
			bar.Finish()
		}
		if err != nil {
			return err
		}
		if transferred.Bytes > 0 && transferred.Chunks > 0 {
			fmt.Printf("Chunked: uploaded %.1f MB of %.1f MB (%.1f%% deduplicated, %d/%d chunks)\n",
				float64(transferred.BytesUploaded)/(1<<20), float64(transferred.Bytes)/(1<<20),
				transferred.Deduplicated()*100, transferred.ChunksUploaded, transferred.Chunks)
		}
		fmt.Println("Done.")
		return nil
	},
}

func init() {
	pushCmd.Flags().BoolVar(&pushForce, "force", false, "Overwrite existing tag even if bucket is immutable")
	rootCmd.AddCommand(pushCmd)
}
