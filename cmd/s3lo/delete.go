package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:     "delete <s3-ref>",
	Short:   "Delete an image tag from S3",
	Example: `  s3lo delete s3://my-bucket/myapp:v1.0`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := image.Delete(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Deleted %s\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
}
