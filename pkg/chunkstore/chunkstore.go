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

	"github.com/OuFinx/s3lo/pkg/chunk"
	storage "github.com/OuFinx/s3lo/pkg/storage"
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

// Recipe is the ordered chunk list that reconstitutes one layer.
type Recipe struct {
	// Layer is the hex sha256 of the assembled layer, without the "sha256:" prefix.
	Layer  string     `json:"layer"`
	Size   int64      `json:"size"`
	Chunks []ChunkRef `json:"chunks"`
}

// ChunkRef identifies one chunk and its length in the assembled layer. Size is
// the uncompressed length; the stored object is compressed and shorter.
type ChunkRef struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
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
// localPath's contents and becomes the recipe's key.
func Store(ctx context.Context, client storage.Backend, bucket, localPath, layerDigest string) (Stats, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return Stats{}, fmt.Errorf("open layer %s: %w", layerDigest, err)
	}
	defer f.Close()

	var (
		mu     sync.Mutex
		stats  Stats
		recipe = Recipe{Layer: layerDigest}
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(chunkConcurrency)

	splitErr := chunk.Split(f, func(data []byte) error {
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		size := int64(len(data))

		mu.Lock()
		recipe.Chunks = append(recipe.Chunks, ChunkRef{Digest: digest, Size: size})
		recipe.Size += size
		stats.Chunks++
		stats.Bytes += size
		mu.Unlock()

		// Split reuses its buffer, so the chunk must be copied before it is
		// handed to a goroutine that outlives this callback.
		payload := make([]byte, len(data))
		copy(payload, data)

		g.Go(func() error {
			key := ChunkKey(digest)
			exists, err := client.HeadObjectExists(gCtx, bucket, key)
			if err != nil {
				return fmt.Errorf("check chunk %s: %w", digest[:12], err)
			}
			if exists {
				return nil
			}
			if err := client.PutObject(gCtx, bucket, key, enc().EncodeAll(payload, nil)); err != nil {
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
		return stats, fmt.Errorf("split layer %s: %w", layerDigest, splitErr)
	}
	if waitErr != nil {
		return stats, waitErr
	}

	recipeData, err := json.Marshal(recipe)
	if err != nil {
		return stats, fmt.Errorf("marshal recipe %s: %w", layerDigest, err)
	}
	if err := client.PutObject(ctx, bucket, RecipeKey(layerDigest), recipeData); err != nil {
		return stats, fmt.Errorf("write recipe %s: %w", layerDigest, err)
	}
	return stats, nil
}

// LoadRecipe reads the recipe for a layer digest. It returns false when the
// layer is not chunked in this bucket, so callers can fall back to a whole-layer
// blob without treating a plain bucket as an error.
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
			stored, err := client.GetObject(gCtx, bucket, ChunkKey(c.Digest))
			if err != nil {
				return fmt.Errorf("fetch chunk %s: %w", c.Digest[:12], err)
			}
			data, err := dec().DecodeAll(stored, nil)
			if err != nil {
				return fmt.Errorf("decompress chunk %s: %w", c.Digest[:12], err)
			}
			if int64(len(data)) != c.Size {
				return fmt.Errorf("chunk %s is %d bytes, recipe says %d",
					c.Digest[:12], len(data), c.Size)
			}
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != c.Digest {
				return fmt.Errorf("chunk %s failed digest check", c.Digest[:12])
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

// Stream writes a chunked layer to w in order, verifying each chunk against its
// digest as it goes. It is the read path for callers that hand bytes straight to
// a client rather than to a file, where seeking is not available.
//
// ponytail: chunks are fetched one at a time, so throughput is bounded by a
// single connection's round trip. A bounded read-ahead window (fetch chunk i+1..
// i+n while writing chunk i) is the upgrade when serve throughput matters.
func Stream(ctx context.Context, client storage.Backend, bucket string, recipe Recipe, w io.Writer) error {
	for _, c := range recipe.Chunks {
		stored, err := client.GetObject(ctx, bucket, ChunkKey(c.Digest))
		if err != nil {
			return fmt.Errorf("fetch chunk %s: %w", c.Digest[:12], err)
		}
		data, err := dec().DecodeAll(stored, nil)
		if err != nil {
			return fmt.Errorf("decompress chunk %s: %w", c.Digest[:12], err)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != c.Digest {
			return fmt.Errorf("chunk %s failed digest check", c.Digest[:12])
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write chunk %s: %w", c.Digest[:12], err)
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
