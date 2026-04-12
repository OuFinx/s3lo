package main

import (
	"fmt"
	"os"

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

// reportExecutionError prints a visible ERROR line (red when stderr is a TTY),
// then a blank line and the command usage when available.
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
	if cmd == nil {
		return
	}
	usage := cmd.UsageString()
	if usage == "" {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, usage)
}

func stderrColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stderr.Fd()))
}
