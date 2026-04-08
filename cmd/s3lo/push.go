package main

import (
	"fmt"

	"github.com/finx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var pushCmd = &cobra.Command{
	Use:   "push <local-image> <s3-ref>",
	Short: "Push a local Docker image to S3",
	Example: `  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Pushing %s to %s...\n", args[0], args[1])
		if err := image.Push(cmd.Context(), args[0], args[1]); err != nil {
			return err
		}
		fmt.Println("Done.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pushCmd)
}
