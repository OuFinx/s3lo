package main

import (
	"os"
)

func main() {
	propagateSilenceErrors(rootCmd)
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		reportExecutionError(cmd, err)
		os.Exit(1)
	}
}
