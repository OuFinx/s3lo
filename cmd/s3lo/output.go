package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// formatCmds records the commands whose --output selects a serialization format.
// `cat --output` names a file to write to, so it deliberately never registers.
var formatCmds = map[*cobra.Command]bool{}

// addOutputFlag gives a command the one --output convention in this CLI.
func addOutputFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or text (default)")
	formatCmds[cmd] = true
}

// outputFormat returns the command's --output value.
func outputFormat(cmd *cobra.Command) string {
	format, _ := cmd.Flags().GetString("output")
	return format
}

// validOutputFormats is the single list every --output is checked against.
// "table" is the old name for "text", kept so existing scripts keep working.
var validOutputFormats = map[string]bool{"": true, "text": true, "table": true, "json": true, "yaml": true}

// checkOutputFormat rejects an unknown --output before the command does any
// work. Validating afterwards would mean a `sign --output yaml` typo still signs
// the image and only then complains, or, as it used to, silently prints text.
func checkOutputFormat(cmd *cobra.Command) error {
	if format := outputFormat(cmd); formatCmds[cmd] && !validOutputFormats[format] {
		return fmt.Errorf("unknown output format %q (valid: json, yaml, text)", format)
	}
	return nil
}

// status prints human progress to stderr. Everything a caller might pipe stays
// on stdout, so `--output json` survives a pipeline.
func status(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

// writeOutput serializes v to stdout as JSON or YAML.
// Returns (true, nil) on success, (false, nil) for the human formats ("", "text",
// "table") which the caller renders itself, or (false, err) for an unknown format
// or an encoding failure.
func writeOutput(format string, v any) (bool, error) {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return true, enc.Encode(emptySliceAsArray(v))
	case "yaml":
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		err := enc.Encode(v)
		enc.Close()
		return true, err
	default:
		if validOutputFormats[format] {
			return false, nil // human format, rendered by the caller
		}
		return false, fmt.Errorf("unknown output format %q (valid: json, yaml, text)", format)
	}
}

// emptySliceAsArray keeps an empty result set rendering as [] rather than null,
// so `s3lo list -o json | jq '.[]'` works on an empty bucket instead of erroring.
func emptySliceAsArray(v any) any {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	return v
}
