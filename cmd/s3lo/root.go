package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

var rootCmd = &cobra.Command{
	Use:   "s3lo",
	Short: "Store and retrieve OCI container images on AWS S3",
	Long:  "s3lo is a CLI tool for pushing, pulling, listing, and inspecting OCI container images stored on AWS S3.",
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print s3lo version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("s3lo %s (%s)\n", version, commit)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
