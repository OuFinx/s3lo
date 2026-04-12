package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list <s3-bucket-path>",
	Short: "List images in an S3 bucket",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/list/

  s3lo list s3://my-bucket/
  s3lo list s3://my-bucket/ --output json | jq '.[].tags'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")
		entries, err := image.List(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		ok, err := writeOutput(outputFmt, entries)
		if err != nil {
			return err
		}
		if !ok {
			if len(entries) == 0 {
				fmt.Println("No images found.")
				return nil
			}
			for _, entry := range entries {
				for _, tag := range entry.Tags {
					fmt.Printf("%s:%s\n", entry.Name, tag)
				}
			}
		}
		return nil
	},
}

func init() {
	listCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	rootCmd.AddCommand(listCmd)
}
