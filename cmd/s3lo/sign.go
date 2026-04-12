package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	// Register KMS providers so awskms:// and hashivault:// scheme dispatch works.
	_ "github.com/sigstore/sigstore/pkg/signature/kms/aws"
	_ "github.com/sigstore/sigstore/pkg/signature/kms/hashivault"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/spf13/cobra"
)

var signCmd = &cobra.Command{
	Use:   "sign <s3-ref>",
	Short: "Sign an image manifest with a cryptographic key",
	Long: `Sign an OCI image manifest stored in S3 with a cryptographic key.

The signature is stored in S3 alongside the manifest and can be verified
with 's3lo verify'. Signing with AWS KMS satisfies FIPS 140-2 requirements
and produces a CloudTrail audit entry for every signing operation.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/sign/

  # Sign with AWS KMS (recommended for production / FedRAMP)
  s3lo sign s3://my-bucket/myapp:v1.0 --key awskms://alias/release-signer

  # Sign with ARN
  s3lo sign s3://my-bucket/myapp:v1.0 --key "awskms:///arn:aws:kms:us-east-1:123456789012:key/mrk-abc"

  # Sign with a local key file (dev / CI)
  COSIGN_PASSWORD=secret s3lo sign s3://my-bucket/myapp:v1.0 --key cosign.key`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireTag(args[0]); err != nil {
			return err
		}
		keyRef, _ := cmd.Flags().GetString("key")
		output, _ := cmd.Flags().GetString("output")

		result, err := image.Sign(cmd.Context(), args[0], keyRef)
		if err != nil {
			return err
		}

		if output == "json" {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
				"digest":     result.Digest,
				"keyRef":     result.KeyRef,
				"keyID":      result.KeyID,
				"signedAt":   result.SignedAt.Format(time.RFC3339),
				"storedPath": result.StoredPath,
			})
		}

		// Text output: show image:tag portion only.
		display := args[0]
		if idx := strings.LastIndex(display, "/"); idx >= 0 {
			display = display[idx+1:]
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Signed %s\n", display)
		fmt.Fprintf(cmd.OutOrStdout(), "  Digest:  %s\n", result.Digest)
		fmt.Fprintf(cmd.OutOrStdout(), "  Key:     %s\n", result.KeyRef)
		fmt.Fprintf(cmd.OutOrStdout(), "  Key ID:  %s\n", result.KeyID)
		fmt.Fprintf(cmd.OutOrStdout(), "  Signed:  %s\n", result.SignedAt.Format(time.RFC3339))
		fmt.Fprintf(cmd.OutOrStdout(), "  Stored:  %s\n", result.StoredPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(signCmd)
	signCmd.Flags().String("key", "", "Signing key: file path, awskms://, or hashivault:// (required)")
	signCmd.Flags().String("output", "text", "Output format: text or json")
	signCmd.MarkFlagRequired("key")
}
