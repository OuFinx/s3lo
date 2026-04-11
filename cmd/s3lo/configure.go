package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/OuFinx/s3lo/pkg/localconfig"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
	"github.com/spf13/cobra"
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Interactive first-time setup",
	Long:  "Guided setup that saves a default bucket and region to ~/.config/s3lo/config.yaml.",
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("Welcome to s3lo!")
		fmt.Println()

		// Region - detect current from environment or use us-east-1 as fallback.
		defaultRegion := os.Getenv("AWS_DEFAULT_REGION")
		if defaultRegion == "" {
			defaultRegion = os.Getenv("AWS_REGION")
		}
		if defaultRegion == "" {
			defaultRegion = "us-east-1"
		}

		fmt.Printf("AWS Region [%s]: ", defaultRegion)
		region, _ := reader.ReadString('\n')
		region = strings.TrimSpace(region)
		if region == "" {
			region = defaultRegion
		}

		fmt.Print("Default S3 bucket (optional, press Enter to skip): ")
		bucket, _ := reader.ReadString('\n')
		bucket = strings.TrimSpace(bucket)

		// Verify connectivity if bucket provided.
		if bucket != "" {
			fmt.Printf("\nVerifying access to s3://%s... ", bucket)
			client, err := s3client.NewClient(cmd.Context())
			if err != nil {
				fmt.Printf("failed (%v)\n", err)
			} else {
				if _, err := client.ClientForBucket(cmd.Context(), bucket); err != nil {
					fmt.Printf("failed (%v)\n", err)
				} else {
					fmt.Println("ok")
				}
			}
		}

		cfg := &localconfig.Config{
			DefaultBucket: bucket,
			Region:        region,
		}

		if err := localconfig.Save(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		p, _ := localconfig.Path()
		fmt.Printf("\nConfiguration saved to %s\n\n", p)

		if bucket != "" {
			fmt.Printf("You can now push images:\n  s3lo push myapp:v1.0 s3://%s/myapp:v1.0\n", bucket)
		} else {
			fmt.Println("You can now push images:\n  s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configureCmd)
}
