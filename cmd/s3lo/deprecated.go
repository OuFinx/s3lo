package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// addDeprecatedAliases keeps the old `bucket …` and `security …` spellings
// working for one release, hidden from help and noisy on stderr.
//
// It must be called from main, not from an init(): the aliases are shallow
// copies of the real commands and only carry their flags once every init() has
// registered them.
func addDeprecatedAliases() {
	bucketCmd := &cobra.Command{Use: "bucket", Short: "Deprecated: use the top-level commands", Hidden: true}
	securityCmd := &cobra.Command{Use: "security", Short: "Deprecated: use the top-level commands", Hidden: true}

	bucketCmd.AddCommand(
		deprecatedAlias("bucket", statsCmd),
		deprecatedAlias("bucket", doctorCmd),
		deprecatedAlias("bucket", cleanCmd),
		// A silent no-op would let CI keep "initialising" a bucket that is never
		// initialised. Fail loudly instead.
		&cobra.Command{
			Use:    "init",
			Hidden: true,
			Args:   cobra.ArbitraryArgs,
			// Swallow --local and friends so the removal message is what the
			// caller sees, not "unknown flag".
			DisableFlagParsing: true,
			RunE: func(*cobra.Command, []string) error {
				return fmt.Errorf("s3lo bucket init was removed: the layout is created on demand, just push")
			},
		},
	)
	securityCmd.AddCommand(
		deprecatedAlias("security", signCmd),
		deprecatedAlias("security", verifyCmd),
	)

	rootCmd.AddCommand(bucketCmd, securityCmd)
}

// deprecatedAlias copies target so the alias shares its RunE, Args, and flag
// set, then warns before delegating.
func deprecatedAlias(group string, target *cobra.Command) *cobra.Command {
	alias := *target
	alias.Hidden = true
	alias.PreRunE = func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(os.Stderr, "Note: \"s3lo %s %s\" is deprecated and will be removed; use \"s3lo %s\".\n",
			group, target.Name(), target.Name())
		return nil
	}
	return &alias
}
