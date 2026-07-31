package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"github.com/OuFinx/s3lo/v2/pkg/storage"
	"github.com/spf13/cobra"
)

var (
	version       = "dev"
	commit        = "none"
	verbose       bool
	endpoint      string
	timeout       time.Duration
	cancelTimeout context.CancelFunc
)

var rootCmd = &cobra.Command{
	Use:   "s3lo",
	Short: "Store and retrieve OCI container images on object storage",
	Long: `s3lo is a CLI tool for pushing, pulling, listing, and inspecting OCI container
images stored on object storage: AWS S3, Google Cloud Storage, Azure Blob
Storage, a local directory, or any S3-compatible service via --endpoint.`,
	// Errors and usage are printed in main (red ERROR, then usage) for clearer separation.
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := checkOutputFormat(cmd); err != nil {
			return err
		}
		if verbose {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})))
		}
		if timeout > 0 {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			cancelTimeout = cancel
			cmd.SetContext(ctx)
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
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if cancelTimeout != nil {
			cancelTimeout()
		}
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
	// Shell completion comes free with cobra and was switched off. Leaving it
	// off costs nothing to build and everything to discover: a CLI that cannot
	// complete its own subcommands reads as unfinished.
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose debug output")
	rootCmd.PersistentFlags().StringVar(&endpoint, "endpoint", "", "Override storage endpoint URL (for MinIO, R2, Ceph)")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 0, "Maximum time for a command to run (e.g. 30m, 2h). Default: no timeout.")
	rootCmd.AddCommand(versionCmd)
}
