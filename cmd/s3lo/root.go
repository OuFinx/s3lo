package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "s3lo",
	Short: "Store and retrieve OCI container images on AWS S3",
	Long:  "s3lo is a CLI tool for pushing, pulling, listing, and inspecting OCI container images stored on AWS S3.",
}
