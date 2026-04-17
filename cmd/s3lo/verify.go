package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify <s3-ref>",
	Short: "Verify an image signature",
	Long: `Verify that a stored image signature matches the current manifest.

Exit codes:
  0  Signature is valid
  1  Signature is missing or invalid (supply-chain check failed)
  2  Error contacting the storage backend or key provider`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/verify/

  # Verify with AWS KMS public key
  s3lo verify s3://my-bucket/myapp:v1.0 --key awskms://alias/release-signer

  # Verify with a local public key file
  s3lo verify s3://my-bucket/myapp:v1.0 --key cosign.pub

  # Machine-readable output for CI
  s3lo verify s3://my-bucket/myapp:v1.0 --key cosign.pub --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		keyRef, _ := cmd.Flags().GetString("key")
		output, _ := cmd.Flags().GetString("output")

		result, err := image.Verify(cmd.Context(), args[0], keyRef)
		if err != nil {
			// Infrastructure failure → exit 2.
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2)
		}

		// Text output: image:tag portion only.
		display := args[0]
		if idx := strings.LastIndex(display, "/"); idx >= 0 {
			display = display[idx+1:]
		}

		if output == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			enc.Encode(result) //nolint:errcheck
			if !result.Verified {
				os.Exit(1)
			}
			return nil
		}

		if !result.Verified {
			fmt.Fprintf(os.Stderr, "✗ Verification FAILED for %s\n", display)
			fmt.Fprintf(os.Stderr, "  %s\n", result.Reason)
			os.Exit(1)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "✓ Verified %s\n", display)
		fmt.Fprintf(cmd.OutOrStdout(), "  Digest:  %s\n", result.Digest)
		fmt.Fprintf(cmd.OutOrStdout(), "  Signed:  %s\n", result.SignedAt)
		fmt.Fprintf(cmd.OutOrStdout(), "  Key:     %s\n", result.KeyRef)
		return nil
	},
}

func init() {
	securityCmd.AddCommand(verifyCmd)
	verifyCmd.Flags().String("key", "", "Verification key: .pub file, awskms://, or hashivault:// (required)")
	verifyCmd.Flags().String("output", "text", "Output format: text or json")
	verifyCmd.MarkFlagRequired("key")
}
