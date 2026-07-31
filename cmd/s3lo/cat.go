package main

import (
	"fmt"
	"os"

	"github.com/OuFinx/s3lo/v3/pkg/image"
	"github.com/spf13/cobra"
)

var (
	catPlatform string
	catOutput   string
)

var catCmd = &cobra.Command{
	Use:   "cat <s3-ref> <path>",
	Short: "Read one file out of an image without pulling it",
	Long: `Read a single file out of an image stored on S3.

On a chunked bucket only the chunks holding that file are fetched, so reading a
config out of a 2 GB image moves a few megabytes. A registry cannot do this: the
OCI Distribution Spec addresses whole layers, so the same read means downloading
the layer.

Layers are searched from the top down and whiteouts are honoured, so what comes
back is what the running container would see at that path.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/cat/

  s3lo cat s3://my-bucket/myapp:v1.0 /etc/os-release
  s3lo cat s3://my-bucket/myapp:v1.0 /app/config.yaml -o config.yaml
  s3lo cat s3://my-bucket/myapp:v1.0 /etc/passwd --platform linux/arm64`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		res, err := image.CatFile(cmd.Context(), args[0], args[1], catPlatform)
		if err != nil {
			return err
		}

		if catOutput == "" {
			_, err := os.Stdout.Write(res.Data)
			return err
		}
		if err := os.WriteFile(catOutput, res.Data, 0o644); err != nil {
			return err
		}
		// Only on -o: writing this to stdout would corrupt the file itself.
		if s := res.Savings(); s != "" {
			fmt.Fprintf(os.Stderr, "Wrote %s (%d bytes, fetched %s)\n", catOutput, len(res.Data), s)
		} else {
			fmt.Fprintf(os.Stderr, "Wrote %s (%d bytes, layer had no file index)\n", catOutput, len(res.Data))
		}
		return nil
	},
}

func init() {
	catCmd.Flags().StringVar(&catPlatform, "platform", "", "Platform to read from a multi-arch image (e.g. linux/arm64)")
	catCmd.Flags().StringVarP(&catOutput, "output", "o", "", "Write to a file instead of stdout")
	rootCmd.AddCommand(catCmd)
}
