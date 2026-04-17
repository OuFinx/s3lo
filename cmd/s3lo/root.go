package main

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"

	"github.com/OuFinx/s3lo/pkg/storage"
	"github.com/spf13/cobra"
)

var (
	version  = "dev"
	commit   = "none"
	verbose  bool
	endpoint string
)

var rootCmd = &cobra.Command{
	Use:   "s3lo",
	Short: "Store and retrieve OCI container images on AWS S3",
	Long:  "s3lo is a CLI tool for pushing, pulling, listing, and inspecting OCI container images stored on AWS S3.",
	// Errors and usage are printed in main (red ERROR, then usage) for clearer separation.
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if verbose {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})))
		}
		if endpoint != "" {
			u, err := url.Parse(endpoint)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("invalid endpoint %q: must be a full URL with http:// or https:// scheme (e.g. https://s3.example.com)", endpoint)
			}
			ctx := storage.WithEndpoint(cmd.Context(), endpoint)
			cmd.SetContext(ctx)
		}
		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print s3lo version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("s3lo %s (%s)\n", version, commit)
	},
}

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose debug output")
	rootCmd.PersistentFlags().StringVar(&endpoint, "endpoint", "", "Override storage endpoint URL (for MinIO, R2, Ceph)")
	rootCmd.AddCommand(versionCmd)
}
