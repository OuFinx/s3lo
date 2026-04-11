package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var recommendCmd = &cobra.Command{
	Use:     "recommend <s3-bucket-ref>",
	Short:   "Generate S3 Lifecycle Rule recommendations for a bucket",
	Example: `  s3lo recommend s3://my-bucket/`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := image.Recommend(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Recommended S3 Lifecycle Rules for %s:\n\n", result.Bucket)
		for i, rec := range result.Recommendations {
			fmt.Printf("%d. %s\n", i+1, rec.Title)
			for _, line := range splitLines(rec.Description) {
				fmt.Printf("   %s\n", line)
			}
			fmt.Println()
		}

		if result.VersioningOn {
			fmt.Println("(Bucket versioning is enabled - non-current version rule included)")
			fmt.Println()
		}

		fmt.Println("Terraform:")
		for _, line := range splitLines(result.TerraformHCL) {
			fmt.Printf("  %s\n", line)
		}
		return nil
	},
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func init() {
	rootCmd.AddCommand(recommendCmd)
}
