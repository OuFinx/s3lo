package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// propagateSilenceErrors marks cmd and all descendants with SilenceErrors so Cobra
// does not print its own "Error:" line (including Find-time errors, which only
// checked the matched subcommand's flag before root's).
func propagateSilenceErrors(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		propagateSilenceErrors(sub)
	}
	cmd.SilenceErrors = true
}

// reportExecutionError prints a visible ERROR line (red when stderr is a TTY).
// The full usage block is only printed for usage errors (bad flags/args/command),
// not for runtime failures like a digest mismatch or a network error, where dumping
// the whole help text is just noise.
func reportExecutionError(cmd *cobra.Command, err error) {
	if err == nil {
		return
	}
	color := stderrColorEnabled()
	if color {
		// Red + bold "ERROR", default color for message (https://no-color.org/)
		fmt.Fprintf(os.Stderr, "\033[31m\033[1mERROR\033[0m %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "ERROR %v\n", err)
	}
	if cmd == nil || !isUsageError(err) {
		return
	}
	usage := cmd.UsageString()
	if usage == "" {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, usage)
}

// isUsageError reports whether err came from Cobra/pflag argument or flag
// validation (as opposed to a command's RunE). Cobra does not type these errors,
// so we match its stable message forms; unmatched errors default to "not a usage
// error" so runtime failures never trigger a usage dump.
// ponytail: heuristic on Cobra's error strings; revisit if Cobra exposes a typed usage error.
func isUsageError(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"unknown command",
		"unknown flag:",
		"unknown shorthand flag:",
		"flag needs an argument:",
		"invalid argument \"",       // pflag: invalid argument "x" for "--flag"
		"arg(s), received",          // ExactArgs / MaximumNArgs / RangeArgs
		"arg(s), only received",     // MinimumNArgs
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func stderrColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// requireTag returns an error if the s3/local ref does not contain an explicit tag.
// Refs like "s3://bucket/image" silently default to ":latest" in ref.Parse,
// which is almost always a mistake — callers should write the tag out explicitly.
func requireTag(s3Ref string) error {
	// Find the last ":" — but skip the "://" in the scheme.
	i := strings.LastIndex(s3Ref, ":")
	if i < 0 || strings.HasPrefix(s3Ref[i:], "://") {
		return fmt.Errorf("missing tag in %q (e.g. %s:latest)", s3Ref, s3Ref)
	}
	return nil
}
