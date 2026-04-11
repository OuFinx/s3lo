package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var pushForce bool

var pushCmd = &cobra.Command{
	Use:     "push <local-image> <s3-ref>",
	Short:   "Push a local Docker image to S3",
	Example: `  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Pushing %s to %s\n", args[0], args[1])
		opts := image.PushOptions{
			Force: pushForce,
			OnBlob: func(digest string, size int64, skipped bool) {
				short := digest
				if len(short) > 19 {
					short = short[:19]
				}
				status := "uploaded"
				if skipped {
					status = "skipped (exists)"
				}
				fmt.Printf("  sha256:%s  %-10s  %s\n", short, formatBytes(size), status)
			},
		}
		if err := image.Push(cmd.Context(), args[0], args[1], opts); err != nil {
			return err
		}
		fmt.Println("Done.")
		return nil
	},
}

func init() {
	pushCmd.Flags().BoolVar(&pushForce, "force", false, "Overwrite existing tag even if bucket is immutable")
	rootCmd.AddCommand(pushCmd)
}
