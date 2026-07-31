package chunkstore

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"golang.org/x/sync/errgroup"
)

// IndexesPrefix holds one file index per chunked layer, keyed the same way as
// the recipe.
const IndexesPrefix = "indexes/sha256/"

// IndexKey returns the object key holding the file index for a layer.
func IndexKey(compressedDigest string) string { return IndexesPrefix + compressedDigest }

// FileIndex records where each file's bytes sit inside a layer's raw tar.
//
// It deliberately does not record which chunks hold a file. Offsets plus the
// recipe's chunk sizes give that answer exactly, and storing it twice would only
// create a second thing that can disagree with the first. Slacker (USENIX FAST
// '16) measured that pulling is 76% of container start time while 6.4% of the
// image is ever read; addressing a file rather than a layer is what this index
// makes possible.
type FileIndex struct {
	// Layer is the raw layer digest the offsets belong to.
	Layer string      `json:"layer"`
	Files []FileEntry `json:"files"`
}

// FileEntry is one entry inside the layer. Whiteout markers are ordinary
// zero-length entries and are recorded like any other, so a reader can see that
// a higher layer deleted a path without fetching anything.
//
// Link is set for a symlink or hard link, in which case Offset and Size mean
// nothing. Links are indexed because leaving them out makes the index lie about
// the image: /etc/os-release is a symlink on every Debian-derived image, and a
// reader that skipped links would report it missing.
type FileEntry struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Link   string `json:"link,omitempty"`
}

// Find returns the entry for path, or false. Both sides are normalised, because
// a layer tar may name the same file "usr/bin/tool", "./usr/bin/tool" or
// "/usr/bin/tool" depending on what built it.
func (ix FileIndex) Find(path string) (FileEntry, bool) {
	want := NormalisePath(path)
	for _, f := range ix.Files {
		if NormalisePath(f.Path) == want {
			return f, true
		}
	}
	return FileEntry{}, false
}

// NormalisePath strips the leading forms a tar entry may carry so two spellings
// of one path compare equal.
func NormalisePath(p string) string {
	for {
		switch {
		case strings.HasPrefix(p, "./"):
			p = p[2:]
		case strings.HasPrefix(p, "/"):
			p = p[1:]
		default:
			return p
		}
	}
}

// countingReader tracks how many bytes the tar reader has consumed.
//
// It must not expose Seek. archive/tar skips over an entry's data with a Seek
// when the underlying reader offers one, and those bytes would then never pass
// through Read — every offset after the first entry would be wrong.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// BuildIndex walks the raw tar at localPath and records where each file's data
// begins. A layer that is not a tar yields no index and no error: chunking has
// to keep working for whatever bytes it is handed.
func BuildIndex(localPath, layerDigest string) (FileIndex, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return FileIndex{}, fmt.Errorf("open layer %s: %w", short(layerDigest), err)
	}
	defer f.Close()

	cr := &countingReader{r: f}
	tr := tar.NewReader(cr)
	ix := FileIndex{Layer: layerDigest}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Not a tar, or a tar this build cannot walk. The layer is still
			// stored and readable; it just has no per-file addressing.
			return FileIndex{}, nil
		}
		switch h.Typeflag {
		case tar.TypeReg:
			// Next has consumed exactly the header, so the reader now sits on
			// the first byte of this entry's data.
			ix.Files = append(ix.Files, FileEntry{Path: h.Name, Offset: cr.n, Size: h.Size})
		case tar.TypeSymlink, tar.TypeLink:
			ix.Files = append(ix.Files, FileEntry{Path: h.Name, Link: h.Linkname})
		}
	}
	return ix, nil
}

// StoreIndex writes the index for a layer, zstd-compressed. A layer with 20k
// files produces a couple of megabytes of JSON, and this object is fetched on
// every per-file read.
func StoreIndex(ctx context.Context, client storage.Backend, bucket, compressedDigest string, ix FileIndex) error {
	if len(ix.Files) == 0 {
		return nil
	}
	data, err := json.Marshal(ix)
	if err != nil {
		return fmt.Errorf("marshal index %s: %w", short(ix.Layer), err)
	}
	return client.PutObject(ctx, bucket, IndexKey(compressedDigest), enc().EncodeAll(data, nil))
}

// LoadIndex reads the index for a layer. It returns false when there is none,
// which is the normal case for a layer stored whole or pushed before indexes
// existed.
func LoadIndex(ctx context.Context, client storage.Backend, bucket, compressedDigest string) (FileIndex, bool, error) {
	exists, err := client.HeadObjectExists(ctx, bucket, IndexKey(compressedDigest))
	if err != nil {
		return FileIndex{}, false, fmt.Errorf("check index %s: %w", short(compressedDigest), err)
	}
	if !exists {
		return FileIndex{}, false, nil
	}
	stored, err := client.GetObject(ctx, bucket, IndexKey(compressedDigest))
	if err != nil {
		return FileIndex{}, false, fmt.Errorf("read index %s: %w", short(compressedDigest), err)
	}
	data, err := dec().DecodeAll(stored, nil)
	if err != nil {
		return FileIndex{}, false, fmt.Errorf("decompress index %s: %w", short(compressedDigest), err)
	}
	var ix FileIndex
	if err := json.Unmarshal(data, &ix); err != nil {
		return FileIndex{}, false, fmt.Errorf("parse index %s: %w", short(compressedDigest), err)
	}
	return ix, true, nil
}

// chunkStarts returns the offset of each chunk within the raw layer.
func chunkStarts(recipe Recipe) []int64 {
	starts := make([]int64, len(recipe.Chunks))
	var at int64
	for i, c := range recipe.Chunks {
		starts[i] = at
		at += c.Size
	}
	return starts
}

// ExtractFile returns one file's bytes, fetching only the chunks its bytes fall
// in. This is the whole point of the index: a 4 MB read out of a 2 GB layer
// moves one or two chunks, not the layer.
func ExtractFile(ctx context.Context, client storage.Backend, bucket string, recipe Recipe, entry FileEntry) ([]byte, error) {
	if entry.Size == 0 {
		return nil, nil
	}
	if entry.Offset < 0 || entry.Offset+entry.Size > recipe.Size {
		return nil, fmt.Errorf("index for %s is inconsistent: %s spans [%d,%d) of a %d byte layer",
			short(recipe.Layer), entry.Path, entry.Offset, entry.Offset+entry.Size, recipe.Size)
	}

	starts := chunkStarts(recipe)
	// First chunk whose end is past the file's start.
	first := sort.Search(len(starts), func(i int) bool {
		return starts[i]+recipe.Chunks[i].Size > entry.Offset
	})
	// First chunk that begins at or after the file's end.
	last := sort.Search(len(starts), func(i int) bool {
		return starts[i] >= entry.Offset+entry.Size
	}) - 1
	if first > last || first >= len(starts) {
		return nil, fmt.Errorf("index for %s places %s outside the recipe", short(recipe.Layer), entry.Path)
	}

	parts := make([][]byte, last-first+1)
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(chunkConcurrency)
	for i := first; i <= last; i++ {
		i := i
		g.Go(func() error {
			data, err := fetchChunk(gCtx, client, bucket, recipe.Chunks[i])
			if err != nil {
				return err
			}
			parts[i-first] = data
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	span := make([]byte, 0, entry.Size+recipe.Chunks[first].Size)
	for _, p := range parts {
		span = append(span, p...)
	}
	lo := entry.Offset - starts[first]
	return span[lo : lo+entry.Size], nil
}

// ChunksFor reports how many chunks a read of entry has to fetch, and how many
// the whole layer holds. It is what lets a caller say what the index saved.
func ChunksFor(recipe Recipe, entry FileEntry) (need, total int) {
	total = len(recipe.Chunks)
	if entry.Size == 0 || total == 0 {
		return 0, total
	}
	starts := chunkStarts(recipe)
	first := sort.Search(len(starts), func(i int) bool {
		return starts[i]+recipe.Chunks[i].Size > entry.Offset
	})
	last := sort.Search(len(starts), func(i int) bool {
		return starts[i] >= entry.Offset+entry.Size
	}) - 1
	if first > last || first >= total {
		return 0, total
	}
	return last - first + 1, total
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
