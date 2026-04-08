package main

import (
	"github.com/finx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var pushCmd = &cobra.Command{
	Use:   "push <local-image> <s3-ref>",
	Short: "Push a local Docker image to S3",
	Example: `  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return image.Push(args[0], args[1])
	},
}

func init() {
	rootCmd.AddCommand(pushCmd)
}
