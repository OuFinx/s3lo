package main

import (
	"fmt"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage bucket configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set <s3-bucket-ref> <key>=<value>",
	Short: "Set a bucket configuration value",
	Long: `Set a configuration value in the bucket's s3lo.yaml config file.

Available keys:
  immutable   true/false   Prevent overwriting existing image tags`,
	Example: `  s3lo config set s3://my-bucket/ immutable=true
  s3lo config set s3://my-bucket/ immutable=false`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, _, err := image.ParseBucketRef(args[0])
		if err != nil {
			return err
		}

		parts := strings.SplitN(args[1], "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid key=value format: %s", args[1])
		}
		key, val := parts[0], parts[1]

		client, err := s3client.NewClient(cmd.Context())
		if err != nil {
			return err
		}

		cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
		if err != nil {
			return err
		}

		switch key {
		case "immutable":
			cfg.Immutable = val == "true" || val == "yes" || val == "1"
		default:
			return fmt.Errorf("unknown config key %q (valid keys: immutable)", key)
		}

		if err := image.SetBucketConfig(cmd.Context(), client, bucket, cfg); err != nil {
			return err
		}

		fmt.Printf("Set %s=%s for %s\n", key, val, args[0])
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:     "get <s3-bucket-ref>",
	Short:   "Show bucket configuration",
	Example: `  s3lo config get s3://my-bucket/`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, _, err := image.ParseBucketRef(args[0])
		if err != nil {
			return err
		}

		client, err := s3client.NewClient(cmd.Context())
		if err != nil {
			return err
		}

		cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
		if err != nil {
			return err
		}

		fmt.Printf("immutable: %v\n", cfg.Immutable)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	rootCmd.AddCommand(configCmd)
}
