package image

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func digestOf(c string) string { return strings.Repeat(c, 64) }

// ageStore backdates every file past the GC grace period so the test exercises
// reachability rather than the grace window.
func ageStore(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		return os.Chtimes(path, old, old)
	})
	if err != nil {
		t.Fatalf("age store: %v", err)
	}
}

// setupChunkedStore builds a bucket holding one chunked image plus orphans:
//
//	manifests/myapp/latest -> config B, layer L (chunked, no whole-layer blob)
//	recipes/L              -> chunks K1, K2      (reachable)
//	recipes/ORPHAN         -> chunk K4           (unreachable)
//	chunks/K3                                    (unreachable, no recipe at all)
func setupChunkedStore(t *testing.T) (string, string) {
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

	storeDir := filepath.Join(parentDir, "chunkstore")

	config, layer := digestOf("b"), digestOf("a")
	k1, k2, k3, k4 := digestOf("1"), digestOf("2"), digestOf("3"), digestOf("4")
	orphanLayer := digestOf("c")

	manifest := fmt.Appendf(nil,
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
			`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:%s","size":10},`+
			`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"sha256:%s","size":20}]}`,
		config, layer)
	writeTestFile(t, filepath.Join(storeDir, "manifests", "myapp", "latest", "manifest.json"), manifest)
	writeTestFile(t, filepath.Join(storeDir, "blobs", "sha256", config), []byte("config"))

	writeRecipe := func(layerDigest string, chunks ...string) {
		r := map[string]any{"layer": layerDigest, "size": len(chunks) * 4}
		var refs []map[string]any
		for _, c := range chunks {
			refs = append(refs, map[string]any{"digest": c, "size": 4})
		}
		r["chunks"] = refs
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(storeDir, "recipes", "sha256", layerDigest), data)
	}
	writeRecipe(layer, k1, k2)
	writeRecipe(orphanLayer, k4)

	for _, c := range []string{k1, k2, k3, k4} {
		writeTestFile(t, filepath.Join(storeDir, "chunks", "sha256", c), []byte("data"))
	}

	ageStore(t, storeDir)
	return "local://./chunkstore/", storeDir
}

// TestGC_KeepsChunksReachableThroughRecipes is the regression guard for the
// failure that would destroy a chunked bucket: chunks are only reachable as
// manifest -> recipe -> chunk, so a GC that stops at manifests deletes all of
// them and every chunked image with them.
func TestGC_KeepsChunksReachableThroughRecipes(t *testing.T) {
	ctx := context.Background()
	ref, storeDir := setupChunkedStore(t)

	result, err := GC(ctx, ref, false)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	// Survivors: config blob, the reachable recipe, and its two chunks.
	mustExist := []string{
		filepath.Join(storeDir, "blobs", "sha256", digestOf("b")),
		filepath.Join(storeDir, "recipes", "sha256", digestOf("a")),
		filepath.Join(storeDir, "chunks", "sha256", digestOf("1")),
		filepath.Join(storeDir, "chunks", "sha256", digestOf("2")),
	}
	for _, p := range mustExist {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("GC deleted a reachable object %s: %v", filepath.Base(p), err)
		}
	}

	// Casualties: the orphan chunk, the orphan recipe, and the chunk only that
	// recipe referenced.
	mustBeGone := []string{
		filepath.Join(storeDir, "chunks", "sha256", digestOf("3")),
		filepath.Join(storeDir, "recipes", "sha256", digestOf("c")),
		filepath.Join(storeDir, "chunks", "sha256", digestOf("4")),
	}
	for _, p := range mustBeGone {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("GC kept unreachable object %s", filepath.Base(p))
		}
	}

	if result.Deleted != len(mustBeGone) {
		t.Errorf("Deleted = %d, want %d", result.Deleted, len(mustBeGone))
	}
	if result.ChunksScanned != 4 {
		t.Errorf("ChunksScanned = %d, want 4", result.ChunksScanned)
	}
	if result.ChunksDeleted != 2 {
		t.Errorf("ChunksDeleted = %d, want 2", result.ChunksDeleted)
	}
}

// TestGC_DryRunLeavesChunkedBucketIntact makes sure the chunk sweep honours the
// dry-run contract that `bucket clean` relies on by default.
func TestGC_DryRunLeavesChunkedBucketIntact(t *testing.T) {
	ctx := context.Background()
	ref, storeDir := setupChunkedStore(t)

	result, err := GC(ctx, ref, true)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.Deleted == 0 {
		t.Fatal("dry run reported nothing to delete, expected the orphans")
	}
	for _, c := range []string{"1", "2", "3", "4"} {
		if _, err := os.Stat(filepath.Join(storeDir, "chunks", "sha256", digestOf(c))); err != nil {
			t.Errorf("dry run deleted chunk %s", c)
		}
	}
}

// TestGC_PlainBucketUnaffectedByChunkSweep guards against the chunk sweep
// misfiring on buckets that have never had a chunked push.
func TestGC_PlainBucketUnaffectedByChunkSweep(t *testing.T) {
	ctx := context.Background()
	ref := setupMultiArchStore(t)

	result, err := GC(ctx, ref, true)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if result.ChunksScanned != 0 || result.ChunksDeleted != 0 {
		t.Fatalf("plain bucket reported chunk activity: scanned=%d deleted=%d",
			result.ChunksScanned, result.ChunksDeleted)
	}
	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1 orphan blob", result.Deleted)
	}
}
