package main

import (
	"fmt"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var historyCmd = &cobra.Command{
	Use:   "history <s3-ref>",
	Short: "Show push history for a bucket or repository",
	Long: `Show push history at two levels:

  Bucket level — all repositories:
    s3lo history s3://my-bucket/
    s3lo history local://./local-s3/

  Repository level — all tags for one image:
    s3lo history s3://my-bucket/myapp
    s3lo history local://./local-s3/alpine`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/history/

  s3lo history s3://my-bucket/                      # all repositories
  s3lo history s3://my-bucket/myapp                  # all tags for myapp
  s3lo history s3://my-bucket/myapp --all            # include overridden pushes
  s3lo history local://./local-s3/                   # local bucket
  s3lo history local://./local-s3/alpine --limit 5
  s3lo history s3://my-bucket/ --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")
		limit, _ := cmd.Flags().GetInt("limit")
		showAll, _ := cmd.Flags().GetBool("all")
		rawRef := args[0]

		_, imageName, err := image.ParseConfigRef(rawRef)
		if err != nil {
			return err
		}

		// Strip tag if present (e.g. "alpine:latest" -> "alpine").
		if i := strings.LastIndex(imageName, ":"); i >= 0 {
			imageName = imageName[:i]
		}

		if imageName == "" {
			return runBucketHistory(cmd, rawRef, outputFmt, limit)
		}
		return runRepoHistory(cmd, rawRef, imageName, outputFmt, limit, showAll)
	},
}

// runBucketHistory shows a summary of all repositories in the bucket (Mode A).
func runBucketHistory(cmd *cobra.Command, bucketRef, outputFmt string, limit int) error {
	summaries, err := image.ListImageHistory(cmd.Context(), bucketRef)
	if err != nil {
		return err
	}

	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	ok, err := writeOutput(outputFmt, summaries)
	if err != nil {
		return err
	}
	if !ok {
		if len(summaries) == 0 {
			fmt.Println("No push history recorded.")
			return nil
		}
		fmt.Printf("%-20s  %-5s  %-20s  %s\n", "REPOSITORY", "TAGS", "LAST PUSHED", "TOTAL SIZE")
		fmt.Println(strings.Repeat("-", 66))
		for _, s := range summaries {
			fmt.Printf("%-20s  %-5d  %-20s  %s\n",
				s.Name,
				s.Tags,
				s.LastPushedAt.Format("2006-01-02 15:04:05"),
				formatBytes(s.TotalSizeBytes),
			)
		}
	}
	return nil
}

// runRepoHistory shows tag push events for a single repository (Mode B).
// By default only the current (latest) push per tag is shown.
// With showAll=true, overridden pushes are included and marked.
func runRepoHistory(cmd *cobra.Command, bucketRef, imageName, outputFmt string, limit int, showAll bool) error {
	entries, err := image.ListTagHistory(cmd.Context(), bucketRef, imageName)
	if err != nil {
		return err
	}

	if !showAll {
		filtered := entries[:0]
		for _, e := range entries {
			if !e.Superseded {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
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
			fmt.Printf("No push history recorded for %s.\n", imageName)
			return nil
		}
		fmt.Printf("%-20s  %-20s  %-10s  %s\n", "TAG", "PUSHED", "SIZE", "DIGEST")
		fmt.Println(strings.Repeat("-", 80))
		for _, e := range entries {
			digest := e.Digest
			if len(digest) > 19 {
				digest = digest[:19] + "..."
			}
			tag := e.Tag
			if e.Superseded {
				tag += " (overridden)"
			}
			fmt.Printf("%-20s  %-20s  %-10s  %s\n",
				tag,
				e.PushedAt.Format("2006-01-02 15:04:05"),
				formatBytes(e.SizeBytes),
				digest,
			)
		}
	}
	return nil
}

func init() {
	historyCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	historyCmd.Flags().Int("limit", 10, "Maximum number of entries to show (0 = all)")
	historyCmd.Flags().Bool("all", false, "Show full push history including overridden tag pushes")
	rootCmd.AddCommand(historyCmd)
}
