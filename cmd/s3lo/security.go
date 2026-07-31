package main

import "github.com/spf13/cobra"

var securityCmd = &cobra.Command{
	Use:   "security",
	Short: "Sign and verify image signatures",
	Long: `Supply-chain security operations: cryptographic signing and verification,
vulnerability scanning with Trivy, and SBOM generation.`,
}

func init() {
	rootCmd.AddCommand(securityCmd)
}
