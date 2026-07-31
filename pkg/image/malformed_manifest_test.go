package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMalformedManifestDoesNotPanic covers a crash shipped in v3.0.0: every
// digest in this package comes out of a manifest — a file in the bucket that a
// client, or an attacker, wrote — and go-digest's Encoded() panics on anything
// without a ":" separator. A manifest with no config field therefore killed
// `stats` and `doctor` with a stack trace: exactly the commands whose job is to
// find corrupt manifests.
func TestMalformedManifestDoesNotPanic(t *testing.T) {
	storeDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(storeDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	manifests := map[string]string{
		"nocfg":     `{"schemaVersion":2,"layers":[]}`,
		"emptydgst": `{"schemaVersion":2,"config":{"digest":""},"layers":[]}`,
		"nosep":     `{"schemaVersion":2,"config":{"digest":"deadbeef"},"layers":[]}`,
		"badlayer":  `{"schemaVersion":2,"config":{"digest":"sha256:` + strings.Repeat("a", 64) + `"},"layers":[{"digest":"nope"}]}`,
	}
	for name, body := range manifests {
		dir := filepath.Join(".", "st", "manifests", name, "v1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()

	// Each of these walks every manifest in the bucket. None may panic; an
	// error is fine, a crash is not.
	if _, err := Stats(ctx, "local://./st/"); err != nil {
		t.Logf("Stats returned an error (acceptable): %v", err)
	}
	if _, err := Doctor(ctx, "local://./st/"); err != nil {
		t.Logf("Doctor returned an error (acceptable): %v", err)
	}
	if _, err := List(ctx, "local://./st/"); err != nil {
		t.Logf("List returned an error (acceptable): %v", err)
	}
	for name := range manifests {
		if _, err := Inspect(ctx, "local://./st/"+name+":v1"); err != nil {
			t.Logf("Inspect(%s) returned an error (acceptable): %v", name, err)
		}
	}
}
