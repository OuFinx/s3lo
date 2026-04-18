package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/OuFinx/s3lo/pkg/image"
	storage "github.com/OuFinx/s3lo/pkg/storage"
	"github.com/OuFinx/s3lo/pkg/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui <s3-bucket-ref>",
	Short: "Interactive terminal UI for browsing and managing images",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/tui/

  s3lo tui s3://my-bucket/
  s3lo tui s3://my-bucket/prefix/`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s3Ref := args[0]

		bucket, prefix, err := image.ParseBucketRef(s3Ref)
		if err != nil {
			return fmt.Errorf("invalid bucket ref: %w", err)
		}

		st, err := storage.NewBackendFromRef(cmd.Context(), s3Ref)
		if err != nil {
			return fmt.Errorf("create storage client: %w", err)
		}

		m := tui.New(cmd.Context(), s3Ref, st, bucket, prefix)
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("tui error: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
