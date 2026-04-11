package main

import (
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/pkg/image"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
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

By default, lifecycle rules are read from the bucket config stored at s3lo.yaml.
Use --config to override with a local file (same YAML format as 's3lo config set').`,
	Example: `  s3lo lifecycle apply s3://my-bucket/                        # use bucket config
  s3lo lifecycle apply s3://my-bucket/ --config override.yaml # use local file
  s3lo lifecycle apply s3://my-bucket/ --confirm               # actually delete`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var cfg *image.BucketConfig

		if lifecycleApplyConfig != "" {
			data, err := os.ReadFile(lifecycleApplyConfig)
			if err != nil {
				return fmt.Errorf("read config file: %w", err)
			}
			cfg, err = image.LoadBucketConfigFromFile(data)
			if err != nil {
				return err
			}
		} else {
			bucket, _, err := image.ParseBucketRef(args[0])
			if err != nil {
				return err
			}
			client, err := s3client.NewClient(cmd.Context())
			if err != nil {
				return err
			}
			cfg, err = image.GetBucketConfig(cmd.Context(), client, bucket)
			if err != nil {
				return err
			}
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
	lifecycleApplyCmd.Flags().StringVar(&lifecycleApplyConfig, "config", "", "Path to BucketConfig YAML file (optional; defaults to bucket's s3lo.yaml)")
	lifecycleApplyCmd.Flags().BoolVar(&lifecycleConfirm, "confirm", false,
		"Actually delete tags (default is dry-run)")
	lifecycleCmd.AddCommand(lifecycleApplyCmd)
	rootCmd.AddCommand(lifecycleCmd)
}
