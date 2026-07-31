package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/OuFinx/s3lo/v3/pkg/image"
)

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = orig
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// An empty result set used to marshal as `null`, which breaks the documented
// `s3lo list --output json | jq '.[]'` on a bucket with no images.
func TestWriteOutput_EmptySliceIsArrayNotNull(t *testing.T) {
	var entries []image.ImageEntry

	out := captureStdout(t, func() {
		ok, err := writeOutput("json", entries)
		if err != nil || !ok {
			t.Fatalf("writeOutput = (%v, %v)", ok, err)
		}
	})

	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("output = %q, want []", strings.TrimSpace(out))
	}
	var decoded []image.ImageEntry
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not a JSON array: %v", err)
	}
}

func TestWriteOutput_Formats(t *testing.T) {
	for _, format := range []string{"", "text", "table"} {
		ok, err := writeOutput(format, struct{}{})
		if ok || err != nil {
			t.Fatalf("writeOutput(%q) = (%v, %v), want (false, nil)", format, ok, err)
		}
	}
	if _, err := writeOutput("xml", struct{}{}); err == nil {
		t.Fatal("writeOutput(\"xml\") = nil error, want a rejection")
	}
}

func TestCheckOutputFormat_RejectsUnknownBeforeTheCommandRuns(t *testing.T) {
	if err := signCmd.Flags().Set("output", "yaml"); err != nil {
		t.Fatal(err)
	}
	if err := checkOutputFormat(signCmd); err != nil {
		t.Fatalf("sign --output yaml rejected: %v", err)
	}

	if err := signCmd.Flags().Set("output", "xml"); err != nil {
		t.Fatal(err)
	}
	if err := checkOutputFormat(signCmd); err == nil {
		t.Fatal("sign --output xml accepted, want a rejection")
	}
	_ = signCmd.Flags().Set("output", "")

	// cat's --output names a file, so it must never be format-checked.
	if err := catCmd.Flags().Set("output", "/tmp/layer.tar"); err != nil {
		t.Fatal(err)
	}
	if err := checkOutputFormat(catCmd); err != nil {
		t.Fatalf("cat --output <file> rejected: %v", err)
	}
	_ = catCmd.Flags().Set("output", "")
}
