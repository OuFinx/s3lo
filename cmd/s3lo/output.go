package main

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// writeOutput serializes v to stdout as JSON or YAML.
// Returns (true, nil) on success, (false, nil) when format is "" or "table" (caller handles display),
// or (false, err) for an unknown format or encoding failure.
func writeOutput(format string, v any) (bool, error) {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return true, enc.Encode(v)
	case "yaml":
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		err := enc.Encode(v)
		enc.Close()
		return true, err
	case "", "table":
		return false, nil
	default:
		return false, fmt.Errorf("unknown output format %q (valid: json, yaml, table)", format)
	}
}
