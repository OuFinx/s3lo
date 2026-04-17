package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init <s3-bucket-ref> | --local <path>",
	Short: "Initialize a bucket or local directory for s3lo use",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/init/

  # Cloud mode — verify access and write default config
  s3lo init s3://my-bucket/

  # Local mode — scaffold a local storage directory (no AWS needed)
  s3lo init --local ~/.s3lo/local`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		localPath, _ := cmd.Flags().GetString("local")

		if localPath != "" {
			return runLocalInit(localPath)
		}

		if len(args) == 0 {
			return fmt.Errorf("provide an S3 bucket reference (s3://bucket/) or --local <path>")
		}

		result, err := image.Init(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		for _, check := range result.Checks {
			statusMark := "✓"
			if !check.OK {
				statusMark = "!"
			}
			fmt.Printf("%s %s\n", statusMark, check.Label)
			if check.Note != "" {
				fmt.Printf("  %s\n", check.Note)
			}
		}

		fmt.Printf("\nYour bucket is ready. Try:\n")
		fmt.Printf("  s3lo push myapp:latest s3://%s/myapp:latest\n", result.Bucket)
		return nil
	},
}

// localInitConfig is the s3lo.yaml written for local mode.
var localInitConfig = `# s3lo local storage configuration
# See: https://oufinx.github.io/s3lo/commands/config/

default:
  lifecycle:
    keep_last: 10
    max_age: 90d
`

func runLocalInit(localPath string) error {
	dirs := []string{
		filepath.Join(localPath, "blobs", "sha256"),
		filepath.Join(localPath, "manifests"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", d, err)
		}
	}

	configPath := filepath.Join(localPath, "s3lo.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte(localInitConfig), 0o644); err != nil {
			return fmt.Errorf("write s3lo.yaml: %w", err)
		}
		fmt.Printf("✓ Created s3lo.yaml with defaults\n")
	} else {
		fmt.Printf("✓ s3lo.yaml already exists — skipped\n")
	}

	fmt.Printf("✓ Created local storage at %s\n", localPath)
	fmt.Printf("\nYour local storage is ready. Try:\n")

	// Build a usable reference hint. local:// only supports relative paths starting
	// with "./" or simple names. For absolute paths, suggest cd + relative form.
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		absPath = localPath
	}
	parent := filepath.Dir(absPath)
	base := filepath.Base(absPath)
	fmt.Printf("  cd %s\n", parent)
	fmt.Printf("  s3lo push myapp:latest local://./%s/myapp:latest\n", base)
	return nil
}

func init() {
	initCmd.Flags().String("local", "", "Initialize a local directory instead of an S3 bucket")
	bucketCmd.AddCommand(initCmd)
}
