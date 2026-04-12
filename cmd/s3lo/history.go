package main

import (
	"fmt"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var historyCmd = &cobra.Command{
	Use:   "history <s3-ref>",
	Short: "Show push history for an image tag",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/history/

  s3lo history s3://my-bucket/myapp:latest
  s3lo history s3://my-bucket/myapp:latest --limit 5
  s3lo history s3://my-bucket/myapp:latest --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")
		limit, _ := cmd.Flags().GetInt("limit")

		entries, err := image.GetHistory(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if limit > 0 && len(entries) > limit {
			entries = entries[:limit]
		}

		ok, err := writeOutput(outputFmt, entries)
		if err != nil {
			return err
		}
		if !ok {
			if len(entries) == 0 {
				fmt.Println("No push history recorded.")
				return nil
			}
			tag := tagFromHistoryRef(args[0])
			fmt.Printf("%-12s  %-20s  %-10s  %s\n", "TAG", "PUSHED", "SIZE", "DIGEST")
			fmt.Println(strings.Repeat("-", 72))
			for _, e := range entries {
				digest := e.Digest
				if len(digest) > 19 {
					digest = digest[:19] + "..."
				}
				fmt.Printf("%-12s  %-20s  %-10s  %s\n",
					tag,
					e.PushedAt.Format("2006-01-02 15:04:05"),
					formatBytes(e.SizeBytes),
					digest,
				)
			}
		}
		return nil
	},
}

// tagFromHistoryRef extracts the tag from an s3 reference like s3://bucket/image:tag.
func tagFromHistoryRef(s3Ref string) string {
	i := strings.LastIndex(s3Ref, ":")
	if i < 0 || strings.HasPrefix(s3Ref[i:], "://") {
		return ""
	}
	return s3Ref[i+1:]
}

func init() {
	historyCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	historyCmd.Flags().Int("limit", 10, "Maximum number of entries to show (0 = all)")
	rootCmd.AddCommand(historyCmd)
}
