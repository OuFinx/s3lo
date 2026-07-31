package chunkstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
)

// instrumentedBackend wraps a real local backend and records how many GetObject
// calls overlap, which is the only way to tell a concurrent read path from a
// sequential one that happens to be fast.
type instrumentedBackend struct {
	storage.Backend

	mu       sync.Mutex
	inFlight int
	peak     int
	failKey  string
	delay    time.Duration
}

func (b *instrumentedBackend) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.peak {
		b.peak = b.inFlight
	}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}()

	if b.delay > 0 {
		select {
		case <-time.After(b.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if b.failKey != "" && key == b.failKey {
		return nil, errors.New("injected storage failure")
	}
	return b.Backend.GetObject(ctx, bucket, key)
}

func (b *instrumentedBackend) peakConcurrency() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// storeStreamLayer writes a multi-chunk layer and returns its recipe.
func storeStreamLayer(t *testing.T, client storage.Backend, bucket string, size int) (Recipe, []byte) {
	t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(99)).Read(data)

	dir := t.TempDir()
	path, digest := writeLayerData(t, dir, "layer.tar", data)
	rec, _, err := Store(context.Background(), client, bucket, path, digest)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	recipe, ok, err := LoadRecipe(context.Background(), client, bucket, rec.CompressedDigest)
	if err != nil || !ok {
		t.Fatalf("LoadRecipe: ok=%v err=%v", ok, err)
	}
	return recipe, data
}

func TestStreamCompressed_EmitsChunksInOrder(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	client := storage.NewLocalClient()

	recipe, want := storeStreamLayer(t, client, bucket, 40<<20)
	if len(recipe.Chunks) < 3 {
		t.Fatalf("need several chunks to prove ordering, got %d", len(recipe.Chunks))
	}

	var got bytes.Buffer
	if err := StreamCompressed(ctx, client, bucket, recipe, &got); err != nil {
		t.Fatalf("StreamCompressed: %v", err)
	}
	decoded, err := dec().DecodeAll(got.Bytes(), nil)
	if err != nil {
		t.Fatalf("decode streamed frames: %v", err)
	}
	if !bytes.Equal(decoded, want) {
		t.Fatalf("streamed %d raw bytes, want %d; concurrent fetch reordered the output",
			len(decoded), len(want))
	}
	if sum := sha256.Sum256(decoded); hex.EncodeToString(sum[:]) != recipe.Layer {
		t.Error("streamed layer does not decode to the recipe's layer digest")
	}
}

// TestStream_FetchesConcurrently is the whole point of the read-ahead window: a
// gzipped blob from a registry arrives on one connection, these do not.
func TestStreamCompressed_FetchesConcurrently(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	local := storage.NewLocalClient()

	recipe, want := storeStreamLayer(t, local, bucket, 40<<20)

	// A small delay per read makes overlap observable; without it the local
	// backend can finish a chunk before the next fetcher is even scheduled.
	inst := &instrumentedBackend{Backend: local, delay: 20 * time.Millisecond}

	var got bytes.Buffer
	if err := StreamCompressed(ctx, inst, bucket, recipe, &got); err != nil {
		t.Fatalf("StreamCompressed: %v", err)
	}
	decoded, err := dec().DecodeAll(got.Bytes(), nil)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(decoded, want) {
		t.Fatal("output differs under concurrency")
	}

	peak := inst.peakConcurrency()
	t.Logf("peak concurrent chunk reads: %d over %d chunks", peak, len(recipe.Chunks))
	if peak < 2 {
		t.Fatalf("peak concurrency %d: chunks are still being fetched one at a time", peak)
	}
	if peak > streamReadAhead {
		t.Fatalf("peak concurrency %d exceeds the read-ahead window %d; memory is unbounded",
			peak, streamReadAhead)
	}
}

func TestStreamCompressed_PropagatesChunkErrors(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	local := storage.NewLocalClient()

	recipe, _ := storeStreamLayer(t, local, bucket, 40<<20)
	inst := &instrumentedBackend{
		Backend: local,
		failKey: ChunkKey(recipe.Chunks[len(recipe.Chunks)-1].Digest),
	}

	err := StreamCompressed(ctx, inst, bucket, recipe, &bytes.Buffer{})
	if err == nil {
		t.Fatal("StreamCompressed ignored a failing chunk read")
	}
}

func TestStreamCompressed_RejectsCorruptedChunk(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	client := storage.NewLocalClient()

	recipe, _ := storeStreamLayer(t, client, bucket, 40<<20)
	victim := recipe.Chunks[0]
	if err := client.PutObject(ctx, bucket, ChunkKey(victim.Digest),
		enc().EncodeAll(bytes.Repeat([]byte{0x7F}, int(victim.Size)), nil)); err != nil {
		t.Fatal(err)
	}

	if err := StreamCompressed(ctx, client, bucket, recipe, &bytes.Buffer{}); err == nil {
		t.Fatal("StreamCompressed served a chunk of the wrong size")
	}
}

func TestStreamCompressed_EmptyRecipe(t *testing.T) {
	var buf bytes.Buffer
	if err := StreamCompressed(context.Background(), storage.NewLocalClient(), t.TempDir(),
		Recipe{Layer: "x"}, &buf); err != nil {
		t.Fatalf("StreamCompressed on an empty recipe: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes for an empty recipe", buf.Len())
	}
}

// failingWriter stops accepting data partway, standing in for a client that
// disconnects mid-pull.
type failingWriter struct {
	remaining int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, fmt.Errorf("client went away")
	}
	f.remaining--
	return len(p), nil
}

func TestStreamCompressed_StopsWhenWriterFails(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	client := storage.NewLocalClient()

	recipe, _ := storeStreamLayer(t, client, bucket, 40<<20)
	if err := StreamCompressed(ctx, client, bucket, recipe, &failingWriter{remaining: 1}); err == nil {
		t.Fatal("StreamCompressed kept going after the writer failed")
	}
}

// TestStreamCompressed_MatchesManifestIdentity pins the contract the image
// manifest now depends on: the concatenated chunk objects must hash to
// CompressedDigest and decode to exactly the raw layer. If either half breaks, a
// client rejects the blob or unpacks the wrong bytes.
func TestStreamCompressed_MatchesManifestIdentity(t *testing.T) {
	ctx := context.Background()
	bucket := t.TempDir()
	client := storage.NewLocalClient()

	recipe, raw := storeStreamLayer(t, client, bucket, 40<<20)
	if recipe.CompressedDigest == "" {
		t.Fatal("recipe carries no compressed identity")
	}

	var out bytes.Buffer
	if err := StreamCompressed(ctx, client, bucket, recipe, &out); err != nil {
		t.Fatalf("StreamCompressed: %v", err)
	}

	if int64(out.Len()) != recipe.CompressedSize {
		t.Errorf("streamed %d bytes, recipe says %d", out.Len(), recipe.CompressedSize)
	}
	if sum := sha256.Sum256(out.Bytes()); hex.EncodeToString(sum[:]) != recipe.CompressedDigest {
		t.Fatal("compressed stream does not hash to the digest the manifest advertises")
	}

	decoded, err := dec().DecodeAll(out.Bytes(), nil)
	if err != nil {
		t.Fatalf("client-side decode of the served stream: %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("served stream decodes to something other than the original layer")
	}
	if sum := sha256.Sum256(decoded); hex.EncodeToString(sum[:]) != recipe.Layer {
		t.Fatal("decoded layer does not match the diff_id the image config carries")
	}

	t.Logf("served %.1f MB compressed for a %.1f MB layer (%.2fx)",
		float64(out.Len())/(1<<20), float64(len(raw))/(1<<20),
		float64(len(raw))/float64(out.Len()))
}
