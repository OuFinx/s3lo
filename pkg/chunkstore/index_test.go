package chunkstore

import (
	"archive/tar"
	"bytes"
	"math/rand"
	"testing"
)

// tarLayer builds a tar holding the named files and returns its bytes.
func tarLayer(t *testing.T, files map[string][]byte, order []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range order {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func randomBytes(size int, seed int64) []byte {
	b := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// TestExtractFile_ReadsOneFileFromManyChunks is the check the whole per-file
// path rests on. Tar offsets are tracked through a reader that must not be
// seekable — if archive/tar ever skips an entry's data without those bytes
// passing through Read, every offset after the first is wrong and the extracted
// file is silently the wrong bytes. Comparing content, not just length, is what
// catches that.
func TestExtractFile_ReadsOneFileFromManyChunks(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()

	// Several files either side of a chunk boundary, so the target is not the
	// first entry and does not start at offset zero of its chunk.
	files := map[string][]byte{
		"usr/lib/big-a.bin":  randomBytes(5<<20, 1),
		"etc/config.yaml":    randomBytes(300<<10, 2),
		"usr/lib/big-b.bin":  randomBytes(7<<20, 3),
		"app/model.safetens": randomBytes(6<<20, 4),
		"etc/os-release":     []byte("NAME=\"s3lo test\"\nVERSION=\"1\"\n"),
	}
	order := []string{"usr/lib/big-a.bin", "etc/config.yaml", "usr/lib/big-b.bin", "app/model.safetens", "etc/os-release"}
	layer := tarLayer(t, files, order)
	path, digest := writeLayerData(t, dir, "layer.tar", layer)

	recipe, _, err := Store(ctx, client, bucket, path, digest)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if len(recipe.Chunks) < 3 {
		t.Fatalf("expected the layer to span several chunks, got %d", len(recipe.Chunks))
	}

	ix, ok, err := LoadIndex(ctx, client, bucket, recipe.CompressedDigest)
	if err != nil || !ok {
		t.Fatalf("load index: ok=%v err=%v", ok, err)
	}

	for _, name := range order {
		entry, found := ix.Find(name)
		if !found {
			t.Fatalf("%s missing from the index", name)
		}
		got, err := ExtractFile(ctx, client, bucket, recipe, entry)
		if err != nil {
			t.Fatalf("extract %s: %v", name, err)
		}
		if !bytes.Equal(got, files[name]) {
			t.Errorf("%s: extracted %d bytes, want %d, content mismatch=%v",
				name, len(got), len(files[name]), !bytes.Equal(got, files[name]))
		}
	}
}

// TestExtractFile_FetchesOnlyTheChunksItNeeds is the claim the feature is sold
// on. A small file inside a large layer must not cost the whole layer.
func TestExtractFile_FetchesOnlyTheChunksItNeeds(t *testing.T) {
	ctx, client, bucket := newBucket(t)
	dir := t.TempDir()

	small := []byte("just a config\n")
	files := map[string][]byte{
		"usr/lib/bulk.bin": randomBytes(20<<20, 7),
		"etc/small.conf":   small,
	}
	order := []string{"usr/lib/bulk.bin", "etc/small.conf"}
	path, digest := writeLayerData(t, dir, "layer.tar", tarLayer(t, files, order))

	recipe, _, err := Store(ctx, client, bucket, path, digest)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ix, ok, err := LoadIndex(ctx, client, bucket, recipe.CompressedDigest)
	if err != nil || !ok {
		t.Fatalf("load index: ok=%v err=%v", ok, err)
	}
	entry, found := ix.Find("etc/small.conf")
	if !found {
		t.Fatal("small file missing from the index")
	}

	need, total := ChunksFor(recipe, entry)
	if total < 4 {
		t.Fatalf("expected a multi-chunk layer, got %d chunks", total)
	}
	if need != 1 {
		t.Errorf("reading a %d byte file touched %d chunks, want 1", len(small), need)
	}

	got, err := ExtractFile(ctx, client, bucket, recipe, entry)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(got, small) {
		t.Errorf("extracted %q, want %q", got, small)
	}
}

// TestBuildIndex_NonTarLayerIsNotAnError keeps chunking working for whatever
// bytes it is handed. A layer that is not a tar simply has no per-file
// addressing; it must not fail the push.
func TestBuildIndex_NonTarLayerIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	path, digest := writeLayerData(t, dir, "blob.bin", randomBytes(2<<20, 11))

	ix, err := BuildIndex(path, digest)
	if err != nil {
		t.Fatalf("BuildIndex on a non-tar returned an error: %v", err)
	}
	if len(ix.Files) != 0 {
		t.Errorf("expected no entries for a non-tar layer, got %d", len(ix.Files))
	}
}

// TestFind_NormalisesPathSpelling covers the three ways a tar may name one file.
func TestFind_NormalisesPathSpelling(t *testing.T) {
	ix := FileIndex{Files: []FileEntry{{Path: "./usr/bin/tool", Offset: 512, Size: 10}}}
	for _, spelling := range []string{"usr/bin/tool", "./usr/bin/tool", "/usr/bin/tool"} {
		if _, ok := ix.Find(spelling); !ok {
			t.Errorf("Find(%q) missed an entry stored as %q", spelling, ix.Files[0].Path)
		}
	}
}

// TestBuildIndex_RecordsLinks covers the case that made the first version of
// this index lie: /etc/os-release is a symlink on every Debian-derived image,
// so an index of regular files only reports it missing.
func TestBuildIndex_RecordsLinks(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("NAME=\"test\"\n")
	if err := tw.WriteHeader(&tar.Header{
		Name: "usr/lib/os-release", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "etc/os-release", Typeflag: tar.TypeSymlink, Linkname: "../usr/lib/os-release",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	path, digest := writeLayerData(t, dir, "layer.tar", buf.Bytes())

	ix, err := BuildIndex(path, digest)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	link, ok := ix.Find("/etc/os-release")
	if !ok {
		t.Fatal("symlink missing from the index")
	}
	if link.Link != "../usr/lib/os-release" {
		t.Errorf("link target = %q, want %q", link.Link, "../usr/lib/os-release")
	}
	if _, ok := ix.Find("usr/lib/os-release"); !ok {
		t.Error("link target missing from the index")
	}
}
