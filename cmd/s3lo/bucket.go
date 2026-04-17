package main

import "github.com/spf13/cobra"

var bucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage bucket-level operations: init, stats, health, and lifecycle",
	Long: `Bucket-wide operations: initialize a bucket, view storage stats,
run health checks, and apply lifecycle rules to prune old tags and blobs.`,
}

func init() {
	rootCmd.AddCommand(bucketCmd)
}
