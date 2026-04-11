package main

import (
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var lifecycleCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Manage declarative image retention policies",
}

var lifecycleApplyConfig string
var lifecycleConfirm bool

var lifecycleApplyCmd = &cobra.Command{
	Use:   "apply <s3-bucket-ref>",
	Short: "Apply a lifecycle policy to a bucket",
	Long: `Apply a declarative lifecycle policy to delete old image tags.

Runs in dry-run mode by default — no deletions are performed.
Use --confirm to actually delete tags that violate the policy.
Blobs are not deleted; run 'gc --confirm' after to reclaim space.

Example policy file (s3lo-lifecycle.yaml):

  rules:
    - match: "*"
      keep_last: 10
      max_age: 90d

    - match: "dev/*"
      max_age: 7d
      keep_tags: ["latest"]

    - match: "myapp"
      keep_last: 5
      keep_tags: ["stable", "latest"]`,
	Example: `  s3lo lifecycle apply s3://my-bucket/ --config lifecycle.yaml
  s3lo lifecycle apply s3://my-bucket/ --config lifecycle.yaml --confirm`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if lifecycleApplyConfig == "" {
			return fmt.Errorf("--config is required")
		}

		data, err := os.ReadFile(lifecycleApplyConfig)
		if err != nil {
			return fmt.Errorf("read config file: %w", err)
		}

		cfg, err := image.ParseLifecycleConfig(data)
		if err != nil {
			return err
		}

		dryRun := !lifecycleConfirm
		result, err := image.ApplyLifecycle(cmd.Context(), args[0], cfg, dryRun)
		if err != nil {
			return err
		}

		if dryRun {
			fmt.Printf("Dry run: %d tag(s) would be deleted (out of %d evaluated)\n",
				result.Deleted, result.Evaluated)
			if result.Deleted > 0 {
				fmt.Println("Run with --confirm to delete them. Then run 'gc --confirm' to reclaim blob storage.")
			}
		} else {
			fmt.Printf("Deleted %d tag(s) (out of %d evaluated)\n", result.Deleted, result.Evaluated)
			if result.Deleted > 0 {
				fmt.Println("Run 'gc --confirm' to reclaim blob storage.")
			}
		}
		return nil
	},
}

func init() {
	lifecycleApplyCmd.Flags().StringVar(&lifecycleApplyConfig, "config", "", "Path to lifecycle policy YAML file (required)")
	lifecycleApplyCmd.Flags().BoolVar(&lifecycleConfirm, "confirm", false,
		"Actually delete tags (default is dry-run)")
	lifecycleCmd.AddCommand(lifecycleApplyCmd)
	rootCmd.AddCommand(lifecycleCmd)
}
