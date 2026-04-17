package main

import (
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var sbomCmd = &cobra.Command{
	Use:   "sbom <s3-ref>",
	Short: "Generate a Software Bill of Materials (SBOM) for an image",
	Long: `Download an image from storage and generate a Software Bill of Materials using Trivy.

Output formats: cyclonedx (default), spdx-json, spdx

Trivy must be installed, or s3lo can install it automatically.
Use --install-trivy to skip the confirmation prompt.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/sbom/

  s3lo sbom s3://my-bucket/myapp:v1.0
  s3lo sbom s3://my-bucket/myapp:v1.0 --format spdx-json
  s3lo sbom s3://my-bucket/myapp:v1.0 --format cyclonedx -o myapp.cdx.json
  s3lo sbom s3://my-bucket/myapp:v1.0 --platform linux/amd64`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		installFlag, _ := cmd.Flags().GetBool("install-trivy")
		format, _ := cmd.Flags().GetString("format")
		platform, _ := cmd.Flags().GetString("platform")
		outputPath, _ := cmd.Flags().GetString("output")

		trivyPath, err := ensureTrivy(cmd.Context(), installFlag)
		if err != nil {
			return err
		}

		// Progress bar goes to stderr when writing SBOM to stdout, to avoid mixing output.
		if outputPath == "" {
			fmt.Fprintf(os.Stderr, "Generating SBOM for %s\n", args[0])
		} else {
			fmt.Printf("Generating SBOM for %s\n", args[0])
		}

		var bar *progressbar.ProgressBar
		opts := image.SBOMOptions{
			Format:     format,
			Platform:   platform,
			OutputPath: outputPath,
			TrivyPath:  trivyPath,
			OnStart: func(total int64) {
				if term.IsTerminal(int(os.Stderr.Fd())) {
					bar = newProgressBar("  downloading", total)
				}
			},
			OnBlob: func(_ string, size int64) {
				if bar != nil {
					bar.Add64(size)
				}
			},
		}

		if err := image.SBOM(cmd.Context(), args[0], opts); err != nil {
			return err
		}
		if bar != nil {
			bar.Finish()
		}
		if outputPath != "" {
			fmt.Printf("SBOM written to %s\n", outputPath)
		}
		return nil
	},
}

func init() {
	securityCmd.AddCommand(sbomCmd)
	sbomCmd.Flags().Bool("install-trivy", false, "Install Trivy automatically without prompting")
	sbomCmd.Flags().String("format", "cyclonedx", `SBOM output format: cyclonedx (default), spdx-json, spdx`)
	sbomCmd.Flags().String("platform", "", `Platform for a multi-arch image (e.g. "linux/amd64")`)
	sbomCmd.Flags().StringP("output", "o", "", "Write SBOM to file instead of stdout")
}
