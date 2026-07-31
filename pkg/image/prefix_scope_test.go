package image

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// digest64 turns a short label into a plausible 64-hex-char digest.
func digest64(c string) string { return strings.Repeat(c, 64) }

func testManifest(configDigest, layerDigest string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":10},`+
		`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:%s","size":20}]}`,
		configDigest, layerDigest))
}

// setupTwoPrefixStore builds a store holding two teams' images under distinct
// name prefixes, plus one blob nothing references. Every blob is backdated past
// the GC grace period so a sweep is free to delete whatever it calls garbage.
//
// team-a/app:v1 -> config aaa, layer bbb
// team-b/app:v1 -> config ccc, layer ddd
// eee           -> orphan
func setupTwoPrefixStore(t *testing.T) string {
	t.Helper()

	parentDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parentDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	storeDir := filepath.Join(parentDir, "mystore")

	writeTestFile(t, filepath.Join(storeDir, "manifests", "team-a", "app", "v1", "manifest.json"),
		testManifest(digest64("a"), digest64("b")))
	writeTestFile(t, filepath.Join(storeDir, "manifests", "team-b", "app", "v1", "manifest.json"),
		testManifest(digest64("c"), digest64("d")))

	old := time.Now().Add(-2 * time.Hour)
	for _, d := range []string{"a", "b", "c", "d", "e"} {
		p := filepath.Join(storeDir, "blobs", "sha256", digest64(d))
		writeTestFile(t, p, []byte("blob-"+d))
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	return "local://./mystore/"
}

func blobExists(t *testing.T, label string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(".", "mystore", "blobs", "sha256", digest64(label)))
	return err == nil
}

// A ref with a trailing path used to be treated as a key prefix, so it matched
// nothing at all and every bucket-level command reported an empty store.
func TestStats_ScopesToNameFilter(t *testing.T) {
	ctx := context.Background()
	ref := setupTwoPrefixStore(t)

	all, err := Stats(ctx, ref)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if all.Images != 2 || all.Tags != 2 {
		t.Fatalf("unscoped Stats = %d images / %d tags, want 2/2", all.Images, all.Tags)
	}

	scoped, err := Stats(ctx, "local://./mystore/team-a/")
	if err != nil {
		t.Fatalf("Stats scoped: %v", err)
	}
	if scoped.Images != 1 || scoped.Tags != 1 {
		t.Fatalf("scoped Stats = %d images / %d tags, want 1/1", scoped.Images, scoped.Tags)
	}
	// Blobs are shared bucket-wide, so they are never narrowed by the filter.
	if scoped.UniqueBlobs != all.UniqueBlobs {
		t.Fatalf("scoped UniqueBlobs = %d, want the bucket-wide %d", scoped.UniqueBlobs, all.UniqueBlobs)
	}
}

func TestList_ScopesToNameFilter(t *testing.T) {
	ctx := context.Background()
	setupTwoPrefixStore(t)

	entries, err := List(ctx, "local://./mystore/team-b/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "team-b/app" {
		t.Fatalf("List = %+v, want just team-b/app", entries)
	}
}

// The one that matters: narrowing which tags a clean touches must never narrow
// the reachability walk, or GC frees blobs another prefix's images still need.
func TestGC_ScopedRefStillSeesEveryManifest(t *testing.T) {
	ctx := context.Background()
	setupTwoPrefixStore(t)

	result, err := GC(ctx, "local://./mystore/team-a/", false)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1 (only the true orphan)", result.Deleted)
	}
	for _, d := range []string{"a", "b", "c", "d"} {
		if !blobExists(t, d) {
			t.Fatalf("blob %s was deleted; a scoped ref must not free another image's blobs", d)
		}
	}
	if blobExists(t, "e") {
		t.Fatal("orphan blob e survived GC")
	}
}

func TestDoctor_ScopedRefCountsOrphansBucketWide(t *testing.T) {
	ctx := context.Background()
	setupTwoPrefixStore(t)

	result, err := Doctor(ctx, "local://./mystore/team-a/")
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if !result.LayoutOK {
		t.Fatal("LayoutOK = false, want true")
	}
	if len(result.ManifestIssues) != 0 {
		t.Fatalf("ManifestIssues = %+v, want none", result.ManifestIssues)
	}
	// Only e is unreferenced. team-b's blobs are live and must not be counted.
	if result.OrphanedBlobs != 1 {
		t.Fatalf("OrphanedBlobs = %d, want 1", result.OrphanedBlobs)
	}
}

func TestApplyLifecycle_ScopesToNameFilter(t *testing.T) {
	ctx := context.Background()
	setupTwoPrefixStore(t)

	// A second, older tag for each team so keep_last: 1 has something to prune.
	old := time.Now().Add(-48 * time.Hour)
	for _, team := range []string{"team-a", "team-b"} {
		p := filepath.Join(".", "mystore", "manifests", team, "app", "v0", "manifest.json")
		writeTestFile(t, p, testManifest(digest64("a"), digest64("b")))
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := LoadBucketConfigFromFile([]byte("default:\n  lifecycle:\n    keep_last: 1\n"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := ApplyLifecycle(ctx, "local://./mystore/team-a/", cfg, true)
	if err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}
	if result.Evaluated != 2 {
		t.Fatalf("Evaluated = %d, want 2 (team-a's tags only)", result.Evaluated)
	}
	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1", result.Deleted)
	}
}
