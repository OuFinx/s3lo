// Package chunkstore stores image layers as content-defined chunks instead of
// one object per layer.
//
// A registry addresses whole layers, so editing one file in a 2 GB layer makes
// the entire layer a new blob. s3lo is not a registry and is not bound by that:
// a layer is split into content-defined chunks (see pkg/chunk), each chunk is
// stored once per bucket under its own digest, and the layer itself becomes an
// ordered list of chunk digests — a recipe.
//
// Layer digests, the image config, and therefore the image ID are untouched.
// Only the way the layer's bytes are laid out in the bucket changes, and Fetch
// verifies the reassembled bytes against the layer digest before returning.
package chunkstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/OuFinx/s3lo/v2/pkg/chunk"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/sync/errgroup"
)

const (
	// ChunksPrefix holds chunk objects, shared across every image in the bucket.
	ChunksPrefix = "chunks/sha256/"
	// RecipesPrefix holds one recipe per chunked layer, keyed by layer digest.
	RecipesPrefix = "recipes/sha256/"

	// chunkConcurrency bounds how many chunks are in flight. Chunks are up to
	// chunk.MaxSize each and are held in memory while in flight, so this is a
	// memory ceiling as much as a throughput knob.
	chunkConcurrency = 4
)

// Recipe is the ordered chunk list that reconstitutes one layer, in both the
// forms the layer can take.
//
// Layer/Size describe the raw tar: that digest is the image config's diff_id and
// never changes. CompressedDigest/CompressedSize describe the concatenation of
// the stored chunk objects, which is itself a valid zstd stream because zstd
// frames concatenate. That second identity is what the image manifest references,
// so a client can be handed the compressed bytes and decompress them natively
// instead of having s3lo decompress on its behalf.
type Recipe struct {
	// Layer is the hex sha256 of the assembled raw layer, without "sha256:".
	Layer string `json:"layer"`
	Size  int64  `json:"size"`
	// CompressedDigest is the hex sha256 of the chunk objects concatenated in
	// order, and is the key this recipe is stored under.
	CompressedDigest string     `json:"compressedDigest"`
	CompressedSize   int64      `json:"compressedSize"`
	Chunks           []ChunkRef `json:"chunks"`
}

// ChunkRef identifies one chunk. Size is its length in the assembled raw layer;
// CompressedSize is the size of the stored object.
type ChunkRef struct {
	Digest         string `json:"digest"`
	Size           int64  `json:"size"`
	CompressedSize int64  `json:"compressedSize"`
}

// Stats reports what a Store call actually transferred, which is the whole point
// of chunking: Uploaded is the part that was not already in the bucket.
type Stats struct {
	Chunks         int
	ChunksUploaded int
	Bytes          int64
	BytesUploaded  int64
}

// Deduplicated returns the fraction of bytes that did not need uploading.
func (s Stats) Deduplicated() float64 {
	if s.Bytes == 0 {
		return 0
	}
	return 1 - float64(s.BytesUploaded)/float64(s.Bytes)
}

var (
	encOnce sync.Once
	encoder *zstd.Encoder
	decOnce sync.Once
	decoder *zstd.Decoder
)

func enc() *zstd.Encoder {
	encOnce.Do(func() {
		// SpeedDefault: push already spends CPU on hashing, and chunks are large
		// enough that a slower level buys little.
		encoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	})
	return encoder
}

func dec() *zstd.Decoder {
	decOnce.Do(func() { decoder, _ = zstd.NewReader(nil) })
	return decoder
}

// RecipeKey returns the object key holding the recipe for a layer digest.
func RecipeKey(layerDigest string) string { return RecipesPrefix + layerDigest }

// ChunkKey returns the object key holding a chunk.
func ChunkKey(chunkDigest string) string { return ChunksPrefix + chunkDigest }

// Store splits localPath into chunks, uploads the ones the bucket does not
// already have, and writes the recipe. layerDigest is the hex sha256 of
// localPath's raw contents. The returned Recipe carries the compressed identity
// the image manifest must reference.
//
// Compression happens on the splitting goroutine rather than in the uploaders,
// because the compressed digest is a hash over the frames in order and cannot be
// assembled out of order. Uploads still run concurrently.
func Store(ctx context.Context, client storage.Backend, bucket, localPath, layerDigest string) (Recipe, Stats, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return Recipe{}, Stats{}, fmt.Errorf("open layer %s: %w", layerDigest, err)
	}
	defer f.Close()

	var (
		mu     sync.Mutex
		stats  Stats
		recipe = Recipe{Layer: layerDigest}
		cHash  = sha256.New()
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(chunkConcurrency)

	splitErr := chunk.Split(f, func(data []byte) error {
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		size := int64(len(data))

		compressed := enc().EncodeAll(data, nil)
		cHash.Write(compressed)

		recipe.Chunks = append(recipe.Chunks, ChunkRef{
			Digest: digest, Size: size, CompressedSize: int64(len(compressed)),
		})
		recipe.Size += size
		recipe.CompressedSize += int64(len(compressed))
		stats.Chunks++
		stats.Bytes += size

		g.Go(func() error {
			key := ChunkKey(digest)
			exists, err := client.HeadObjectExists(gCtx, bucket, key)
			if err != nil {
				return fmt.Errorf("check chunk %s: %w", digest[:12], err)
			}
			if exists {
				return nil
			}
			if err := client.PutObject(gCtx, bucket, key, compressed); err != nil {
				return fmt.Errorf("upload chunk %s: %w", digest[:12], err)
			}
			mu.Lock()
			stats.ChunksUploaded++
			stats.BytesUploaded += size
			mu.Unlock()
			return nil
		})
		return nil
	})

	// Wait before reporting the split error so in-flight uploads are not
	// abandoned mid-write.
	waitErr := g.Wait()
	if splitErr != nil {
		return recipe, stats, fmt.Errorf("split layer %s: %w", layerDigest, splitErr)
	}
	if waitErr != nil {
		return recipe, stats, waitErr
	}

	recipe.CompressedDigest = hex.EncodeToString(cHash.Sum(nil))

	// The index goes in before the recipe, for the same reason chunks do: the
	// recipe is what makes the layer resolvable, so everything a reader may want
	// alongside it should already be there when it appears.
	ix, err := BuildIndex(localPath, layerDigest)
	if err != nil {
		return recipe, stats, err
	}
	if err := StoreIndex(ctx, client, bucket, recipe.CompressedDigest, ix); err != nil {
		return recipe, stats, err
	}

	recipeData, err := json.Marshal(recipe)
	if err != nil {
		return recipe, stats, fmt.Errorf("marshal recipe %s: %w", layerDigest, err)
	}
	// Keyed by the compressed digest because that is what the manifest points at.
	if err := client.PutObject(ctx, bucket, RecipeKey(recipe.CompressedDigest), recipeData); err != nil {
		return recipe, stats, fmt.Errorf("write recipe %s: %w", layerDigest, err)
	}
	return recipe, stats, nil
}

// LoadRecipe reads the recipe stored under digest. The key is the layer's
// compressed digest, which is exactly what an image manifest references, so
// callers can pass a manifest layer digest straight through.
//
// It returns false when there is no recipe, so a caller can fall back to a
// whole-layer blob without treating a plain bucket as an error.
func LoadRecipe(ctx context.Context, client storage.Backend, bucket, layerDigest string) (Recipe, bool, error) {
	exists, err := client.HeadObjectExists(ctx, bucket, RecipeKey(layerDigest))
	if err != nil {
		return Recipe{}, false, fmt.Errorf("check recipe %s: %w", layerDigest, err)
	}
	if !exists {
		return Recipe{}, false, nil
	}
	data, err := client.GetObject(ctx, bucket, RecipeKey(layerDigest))
	if err != nil {
		return Recipe{}, false, fmt.Errorf("read recipe %s: %w", layerDigest, err)
	}
	var r Recipe
	if err := json.Unmarshal(data, &r); err != nil {
		return Recipe{}, false, fmt.Errorf("parse recipe %s: %w", layerDigest, err)
	}
	return r, true, nil
}

// Fetch reassembles a chunked layer into destPath and verifies that the result
// hashes to recipe.Layer. Chunks are fetched concurrently and written at their
// own offsets, so peak memory is bounded by chunkConcurrency, not layer size.
func Fetch(ctx context.Context, client storage.Backend, bucket string, recipe Recipe, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	// Offsets are known up front from the recipe, so chunks can land in any order.
	offsets := make([]int64, len(recipe.Chunks))
	var at int64
	for i, c := range recipe.Chunks {
		offsets[i] = at
		at += c.Size
	}
	if at != recipe.Size {
		return fmt.Errorf("recipe %s is inconsistent: chunks total %d, recorded size %d",
			recipe.Layer, at, recipe.Size)
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(chunkConcurrency)
	for i, c := range recipe.Chunks {
		i, c := i, c
		g.Go(func() error {
			data, err := fetchChunk(gCtx, client, bucket, c)
			if err != nil {
				return err
			}
			if _, err := f.WriteAt(data, offsets[i]); err != nil {
				return fmt.Errorf("write chunk %s: %w", c.Digest[:12], err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if err := verifyFile(destPath, recipe.Layer); err != nil {
		return err
	}
	return nil
}

// streamReadAhead is how many chunks may be fetched and held ahead of the
// writer. It bounds both concurrency and memory: at most this many chunks, each
// up to chunk.MaxSize, exist at once.
const streamReadAhead = 4

// fetchChunk retrieves one chunk, decompresses it and checks it against its
// digest.
func fetchChunk(ctx context.Context, client storage.Backend, bucket string, c ChunkRef) ([]byte, error) {
	stored, err := client.GetObject(ctx, bucket, ChunkKey(c.Digest))
	if err != nil {
		return nil, fmt.Errorf("fetch chunk %s: %w", c.Digest[:12], err)
	}
	data, err := dec().DecodeAll(stored, nil)
	if err != nil {
		return nil, fmt.Errorf("decompress chunk %s: %w", c.Digest[:12], err)
	}
	if int64(len(data)) != c.Size {
		return nil, fmt.Errorf("chunk %s is %d bytes, recipe says %d", c.Digest[:12], len(data), c.Size)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != c.Digest {
		return nil, fmt.Errorf("chunk %s failed digest check", c.Digest[:12])
	}
	return data, nil
}

type chunkResult struct {
	data []byte
	err  error
}

// StreamCompressed writes the layer's chunk objects to w exactly as stored, in
// order, without decompressing anything.
//
// The result is a valid zstd stream whose digest is recipe.CompressedDigest,
// which is what the image manifest advertises. This is the fast read path: it
// moves roughly a third of the bytes the raw form does, and the decompression
// happens in the client that was always going to decompress a layer anyway.
func StreamCompressed(ctx context.Context, client storage.Backend, bucket string, recipe Recipe, w io.Writer) error {
	if len(recipe.Chunks) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]chan chunkResult, len(recipe.Chunks))
	for i := range results {
		results[i] = make(chan chunkResult, 1)
	}
	window := make(chan struct{}, streamReadAhead)

	go func() {
		for i, c := range recipe.Chunks {
			select {
			case window <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(i int, c ChunkRef) {
				data, err := client.GetObject(ctx, bucket, ChunkKey(c.Digest))
				if err != nil {
					err = fmt.Errorf("fetch chunk %s: %w", c.Digest[:12], err)
				}
				results[i] <- chunkResult{data: data, err: err}
			}(i, c)
		}
	}()

	for i, c := range recipe.Chunks {
		select {
		case res := <-results[i]:
			if res.err != nil {
				return res.err
			}
			// The stored object is opaque here, so the only cheap check available
			// is its length; the client verifies the whole stream against the
			// manifest digest, which is the real guarantee.
			if c.CompressedSize > 0 && int64(len(res.data)) != c.CompressedSize {
				return fmt.Errorf("chunk %s is %d stored bytes, recipe says %d",
					c.Digest[:12], len(res.data), c.CompressedSize)
			}
			if _, err := w.Write(res.data); err != nil {
				return fmt.Errorf("write chunk %d of layer %s: %w", i, recipe.Layer, err)
			}
			<-window
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// verifyFile is the reason chunking is safe to enable: a layer assembled from
// the wrong chunks, in the wrong order, is caught here rather than handed to the
// container runtime.
func verifyFile(path, wantDigest string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("reopen %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantDigest {
		return fmt.Errorf("assembled layer digest %s does not match expected %s", got, wantDigest)
	}
	return nil
}
