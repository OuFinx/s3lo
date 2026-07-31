package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/OuFinx/s3lo/pkg/chunk"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

func writeLayerFile(t *testing.T, dir, name string, size int, seed int64) (string, string, []byte) {
	t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(data)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:]), data
}

// TestStoreFetchLayer_BothModes covers the two ways a bucket can hold a layer and
// the guarantee that callers do not have to care which: fetchLayer resolves
// either form and verifies the digest in both.
func TestStoreFetchLayer_BothModes(t *testing.T) {
	ctx := context.Background()
	client := storage.NewLocalClient()

	for _, tc := range []struct {
		name        string
		chunked     bool
		size        int
		wantChunked bool
	}{
		{"chunked bucket, large layer", true, 3 * chunk.MinSize, true},
		{"chunked bucket, sub-chunk layer", true, 1024, false},
		{"plain bucket, large layer", false, 3 * chunk.MinSize, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket := t.TempDir()
			dir := t.TempDir()
			src, digest, want := writeLayerFile(t, dir, "layer.tar", tc.size, 11)

			rec, stats, skipped, err := storeLayer(ctx, client, bucket, src, digest, int64(tc.size), tc.chunked)
			if err != nil {
				t.Fatalf("storeLayer: %v", err)
			}
			if skipped {
				t.Fatal("first store reported the layer as already present")
			}
			if got := stats.Chunks > 0; got != tc.wantChunked {
				t.Fatalf("chunked storage = %v, want %v (stats %+v)", got, tc.wantChunked, stats)
			}

			// A chunked layer must NOT leave a whole-layer blob behind, and a plain
			// one must not leave a recipe.
			blobExists, err := client.HeadObjectExists(ctx, bucket, blobKey(digest))
			if err != nil {
				t.Fatal(err)
			}
			if blobExists == tc.wantChunked {
				t.Errorf("whole-layer blob present = %v, want %v", blobExists, !tc.wantChunked)
			}

			outDir := t.TempDir()
			lookup := digest
			if rec.CompressedDigest != "" {
				lookup = rec.CompressedDigest
			}
			rawDigest, _, err := fetchLayer(ctx, client, bucket, lookup, outDir)
			if err != nil {
				t.Fatalf("fetchLayer: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(outDir, rawDigest))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("layer round trip changed the bytes")
			}

			// Storing it again must be a no-op transfer.
			_, _, skipped, err = storeLayer(ctx, client, bucket, src, digest, int64(tc.size), tc.chunked)
			if err != nil {
				t.Fatalf("second storeLayer: %v", err)
			}
			if !skipped {
				t.Error("re-storing an identical layer uploaded it again")
			}
		})
	}
}

// TestStoreLayer_ChunkedRePushUploadsOnlyTheEdit is the end-to-end version of the
// claim: rebuild an image with a small change and only the changed chunk moves.
func TestStoreLayer_ChunkedRePushUploadsOnlyTheEdit(t *testing.T) {
	ctx := context.Background()
	client := storage.NewLocalClient()
	bucket := t.TempDir()
	dir := t.TempDir()

	size := 16 * chunk.MinSize
	src, digest, original := writeLayerFile(t, dir, "v1.tar", size, 12)
	if _, _, _, err := storeLayer(ctx, client, bucket, src, digest, int64(size), true); err != nil {
		t.Fatalf("store v1: %v", err)
	}

	mid := len(original) / 2
	edited := append(append(append([]byte{}, original[:mid]...), []byte("EDIT")...), original[mid:]...)
	src2 := filepath.Join(dir, "v2.tar")
	if err := os.WriteFile(src2, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(edited)
	digest2 := hex.EncodeToString(sum[:])

	rec2, stats, _, err := storeLayer(ctx, client, bucket, src2, digest2, int64(len(edited)), true)
	if err != nil {
		t.Fatalf("store v2: %v", err)
	}

	t.Logf("re-push after a 4-byte edit: %d/%d chunks, %.1f MB of %.1f MB (%.1f%% deduplicated)",
		stats.ChunksUploaded, stats.Chunks,
		float64(stats.BytesUploaded)/(1<<20), float64(stats.Bytes)/(1<<20),
		stats.Deduplicated()*100)

	if stats.ChunksUploaded == 0 {
		t.Fatal("expected the edited chunk to be uploaded")
	}
	if stats.ChunksUploaded > 2 {
		t.Errorf("a 4-byte edit re-uploaded %d chunks; boundaries are not resynchronizing",
			stats.ChunksUploaded)
	}

	// And the edited layer must still come back byte-exact from the mixed chunks.
	outDir := t.TempDir()
	rawDigest, _, err := fetchLayer(ctx, client, bucket, rec2.CompressedDigest, outDir)
	if err != nil {
		t.Fatalf("fetchLayer v2: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(outDir, rawDigest))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, edited) {
		t.Fatal("edited layer did not round trip")
	}
}
