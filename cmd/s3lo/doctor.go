package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor <s3-bucket-ref>",
	Short: "Check bucket health and report integrity issues",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/doctor/

  s3lo doctor s3://my-bucket/
  s3lo doctor s3://my-bucket/ --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")
		result, err := image.Doctor(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		ok, err := writeOutput(outputFmt, result)
		if err != nil {
			return err
		}
		if !ok {
			printDoctorResult(result)
		}
		if len(result.ManifestIssues) > 0 {
			return fmt.Errorf("bucket has %d manifest issue(s)", len(result.ManifestIssues))
		}
		return nil
	},
}

func printDoctorResult(r *image.DoctorResult) {
	fmt.Printf("Checking bucket: %s\n\n", r.Bucket)

	okStr := func(ok bool) string {
		if ok {
			return "OK"
		}
		return "issues found"
	}

	fmt.Printf("Checking layout structure...    %s\n", okStr(r.LayoutOK))
	fmt.Printf("Checking config (s3lo.yaml)...  %s\n", okStr(r.ConfigOK))

	if len(r.ManifestIssues) == 0 {
		fmt.Printf("Checking manifest integrity...  OK\n")
	} else {
		fmt.Printf("Checking manifest integrity...  %d issue(s)\n", len(r.ManifestIssues))
		for _, issue := range r.ManifestIssues {
			fmt.Printf("  ✗ %s — %s\n", issue.Image, issue.Message)
		}
	}

	if r.OrphanedBlobs == 0 {
		fmt.Printf("Checking for orphaned blobs...  none\n")
	} else {
		fmt.Printf("Checking for orphaned blobs...  %d blobs (%s)\n", r.OrphanedBlobs, formatBytes(r.OrphanedBytes))
		fmt.Printf("  Note: clean skips blobs uploaded within the last hour (grace period).\n")
	}

	if len(r.ManifestIssues) > 0 || r.OrphanedBlobs > 0 {
		fmt.Println()
		if len(r.ManifestIssues) > 0 {
			fmt.Println("Corrupted images found. Run:")
			for _, issue := range r.ManifestIssues {
				fmt.Printf("  s3lo delete %s%s/%s\n", r.Scheme, r.Bucket, issue.Image)
			}
		}
		if r.OrphanedBlobs > 0 {
			fmt.Printf("  s3lo clean %s%s/ --blobs --confirm\n", r.Scheme, r.Bucket)
		}
	}
}

func init() {
	doctorCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	bucketCmd.AddCommand(doctorCmd)
}
