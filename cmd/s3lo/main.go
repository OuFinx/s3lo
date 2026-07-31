package main

import (
	"os"
)

func main() {
	addDeprecatedAliases()
	propagateSilenceErrors(rootCmd)
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		reportExecutionError(cmd, err)
		os.Exit(1)
	}
}
