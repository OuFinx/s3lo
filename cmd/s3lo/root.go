package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	verbose bool
)

var rootCmd = &cobra.Command{
	Use:   "s3lo",
	Short: "Store and retrieve OCI container images on AWS S3",
	Long:  "s3lo is a CLI tool for pushing, pulling, listing, and inspecting OCI container images stored on AWS S3.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if verbose {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})))
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
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Enable verbose debug output")
	rootCmd.AddCommand(versionCmd)
}
