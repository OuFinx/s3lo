package main

import (
	"fmt"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate <s3-bucket-ref>",
	Short: "Migrate images from v1.0.0 to v1.1.0 layout",
	Long: `Convert images from the v1.0.0 per-tag layout (image/tag/blobs/)
to the v1.1.0 global blob layout (blobs/sha256/ + manifests/).

Safe to run multiple times (idempotent).`,
	Example: `  s3lo migrate s3://my-bucket/`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := image.Migrate(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Migrated %d image tag(s), %d blob(s) moved to global store\n",
			result.Images, result.BlobsMoved)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
