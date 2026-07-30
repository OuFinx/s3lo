package chunkstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// newBucket returns a Backend backed by a temp directory, so these tests need no
// cloud account and no MinIO.
func newBucket(t *testing.T) (context.Context, storage.Backend, string) {
	t.Helper()
	return context.Background(), storage.NewLocalClient(), t.TempDir()
}

// writeLayer writes size bytes of deterministic data and returns path and digest.
func writeLayer(t *testing.T, dir, name string, size int, seed int64) (string, string) {
	t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(data)
	return writeLayerData(t, dir, name, data)
}

func writeLayerData(t *testing.T, dir, name string, data []byte) (string, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:])
}

func readLayer(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestStore_FetchRoundTrip(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()
	src, digest := writeLayer(t, dir, "layer.tar", 40<<20, 1)

	stats, err := Store(ctx, client, bucket, src, digest)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if stats.Chunks < 2 {
		t.Fatalf("expected the layer to split into several chunks, got %d", stats.Chunks)
	}

	recipe, ok, err := LoadRecipe(ctx, client, bucket, digest)
	if err != nil || !ok {
		t.Fatalf("LoadRecipe: ok=%v err=%v", ok, err)
	}

	dst := filepath.Join(dir, "assembled.tar")
	if err := Fetch(ctx, client, bucket, recipe, dst); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(readLayer(t, src), readLayer(t, dst)) {
		t.Fatal("assembled layer differs from the original")
	}
}

func TestLoadRecipe_MissingIsNotAnError(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	_, ok, err := LoadRecipe(ctx, client, bucket, "0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("LoadRecipe on a plain bucket should not error: %v", err)
	}
	if ok {
		t.Fatal("LoadRecipe reported a recipe that was never written")
	}
}

func TestStore_IdenticalLayerUploadsNothingTwice(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()
	src, digest := writeLayer(t, dir, "layer.tar", 40<<20, 2)

	first, err := Store(ctx, client, bucket, src, digest)
	if err != nil {
		t.Fatalf("first Store: %v", err)
	}
	if first.ChunksUploaded != first.Chunks {
		t.Fatalf("first push should upload every chunk: %d of %d", first.ChunksUploaded, first.Chunks)
	}

	second, err := Store(ctx, client, bucket, src, digest)
	if err != nil {
		t.Fatalf("second Store: %v", err)
	}
	if second.ChunksUploaded != 0 || second.BytesUploaded != 0 {
		t.Fatalf("re-pushing an identical layer uploaded %d chunks / %d bytes, want 0",
			second.ChunksUploaded, second.BytesUploaded)
	}
}

// TestStore_EditedLayerUploadsOnlyChangedChunks is the claim the whole package
// exists to make: a registry would re-upload the entire layer for this edit.
func TestStore_EditedLayerUploadsOnlyChangedChunks(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()

	original := make([]byte, 64<<20)
	rand.New(rand.NewSource(3)).Read(original)
	src, digest := writeLayerData(t, dir, "v1.tar", original)

	if _, err := Store(ctx, client, bucket, src, digest); err != nil {
		t.Fatalf("Store v1: %v", err)
	}

	// Same layer with a handful of bytes inserted in the middle.
	mid := len(original) / 2
	edited := make([]byte, 0, len(original)+9)
	edited = append(edited, original[:mid]...)
	edited = append(edited, []byte("CHANGED!!")...)
	edited = append(edited, original[mid:]...)
	src2, digest2 := writeLayerData(t, dir, "v2.tar", edited)

	stats, err := Store(ctx, client, bucket, src2, digest2)
	if err != nil {
		t.Fatalf("Store v2: %v", err)
	}

	ratio := float64(stats.BytesUploaded) / float64(stats.Bytes)
	t.Logf("second push: %d/%d chunks, %.1f MB of %.1f MB uploaded (%.1f%% deduplicated)",
		stats.ChunksUploaded, stats.Chunks,
		float64(stats.BytesUploaded)/(1<<20), float64(stats.Bytes)/(1<<20),
		stats.Deduplicated()*100)

	// A 9-byte edit should cost one chunk, not the layer. Allow two chunks'
	// worth of slack for boundary jitter around the edit.
	maxRatio := float64(2*chunkMaxForTest) / float64(len(edited))
	if ratio > maxRatio {
		t.Fatalf("uploaded %.1f%% of the layer after a 9-byte edit (want <= %.1f%%); "+
			"chunks are not being reused", ratio*100, maxRatio*100)
	}

	// The edited layer must still reassemble correctly from the mixed chunk set.
	recipe, ok, err := LoadRecipe(ctx, client, bucket, digest2)
	if err != nil || !ok {
		t.Fatalf("LoadRecipe v2: ok=%v err=%v", ok, err)
	}
	dst := filepath.Join(dir, "v2-assembled.tar")
	if err := Fetch(ctx, client, bucket, recipe, dst); err != nil {
		t.Fatalf("Fetch v2: %v", err)
	}
	if !bytes.Equal(edited, readLayer(t, dst)) {
		t.Fatal("edited layer did not reassemble correctly")
	}
}

// chunkMaxForTest mirrors chunk.MaxSize without importing it, keeping the
// tolerance above tied to the largest a single chunk can be.
const chunkMaxForTest = 16 << 20

func TestFetch_DetectsCorruptedChunk(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()
	src, digest := writeLayer(t, dir, "layer.tar", 40<<20, 4)

	if _, err := Store(ctx, client, bucket, src, digest); err != nil {
		t.Fatalf("Store: %v", err)
	}
	recipe, _, err := LoadRecipe(ctx, client, bucket, digest)
	if err != nil {
		t.Fatalf("LoadRecipe: %v", err)
	}

	// Replace one chunk's bytes with a validly-compressed but wrong payload.
	victim := recipe.Chunks[len(recipe.Chunks)/2]
	if err := client.PutObject(ctx, bucket, ChunkKey(victim.Digest),
		enc().EncodeAll(bytes.Repeat([]byte{0xAA}, int(victim.Size)), nil)); err != nil {
		t.Fatalf("corrupt chunk: %v", err)
	}

	err = Fetch(ctx, client, bucket, recipe, filepath.Join(dir, "out.tar"))
	if err == nil {
		t.Fatal("Fetch accepted a corrupted chunk")
	}
	t.Logf("rejected as expected: %v", err)
}

func TestFetch_DetectsReorderedRecipe(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()
	src, digest := writeLayer(t, dir, "layer.tar", 40<<20, 5)

	if _, err := Store(ctx, client, bucket, src, digest); err != nil {
		t.Fatalf("Store: %v", err)
	}
	recipe, _, err := LoadRecipe(ctx, client, bucket, digest)
	if err != nil {
		t.Fatalf("LoadRecipe: %v", err)
	}
	if len(recipe.Chunks) < 2 {
		t.Skip("layer did not split into enough chunks to reorder")
	}

	// Every chunk is individually valid; only the order is wrong. This is caught
	// by the whole-layer digest check, not the per-chunk one.
	swapped := recipe
	swapped.Chunks = append([]ChunkRef(nil), recipe.Chunks...)
	swapped.Chunks[0], swapped.Chunks[1] = swapped.Chunks[1], swapped.Chunks[0]

	if err := Fetch(ctx, client, bucket, swapped, filepath.Join(dir, "out.tar")); err == nil {
		t.Fatal("Fetch accepted a reordered recipe")
	}
}

func TestRecipe_JSONRoundTrip(t *testing.T) {
	in := Recipe{Layer: "abc", Size: 3, Chunks: []ChunkRef{{Digest: "d1", Size: 1}, {Digest: "d2", Size: 2}}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Recipe
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Layer != in.Layer || out.Size != in.Size || len(out.Chunks) != len(in.Chunks) {
		t.Fatalf("recipe did not survive a JSON round trip: %+v", out)
	}
}
