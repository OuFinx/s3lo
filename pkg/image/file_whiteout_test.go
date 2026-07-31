package image

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
)

// TestWhiteoutsFor_CoversAncestors pins the fix for a wrong-bytes bug: a
// deletion is recorded next to what was deleted, and deleting a directory
// deletes everything under it. "RUN rm -rf /data" emits one ".wh.data" entry,
// not one marker per file, so checking only the file's own marker meant
// `s3lo cat` printed the contents of a file the running container reports as
// missing — with exit 0.
func TestWhiteoutsFor_CoversAncestors(t *testing.T) {
	got := whiteoutsFor("data/keep.txt")
	for _, want := range []string{
		"data/.wh.keep.txt", // the file itself
		".wh.data",          // rm -rf /data
		"data/.wh..wh..opq", // opaque marker on the containing dir
	} {
		if !containsString(got, want) {
			t.Errorf("whiteoutsFor(data/keep.txt) missing %q; got %v", want, got)
		}
	}

	deep := whiteoutsFor("a/b/c.txt")
	for _, want := range []string{"a/b/.wh.c.txt", "a/.wh.b", ".wh.a"} {
		if !containsString(deep, want) {
			t.Errorf("whiteoutsFor(a/b/c.txt) missing %q; got %v", want, deep)
		}
	}

	// A root-level file has no ancestor to whiteout.
	if root := whiteoutsFor("keep.txt"); !containsString(root, ".wh.keep.txt") {
		t.Errorf("whiteoutsFor(keep.txt) = %v, want it to contain .wh.keep.txt", root)
	}
}

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// writeTar builds a layer tar. A nil body means a directory-ish marker entry.
func writeTar(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "layer.tar")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanLayerTar_AncestorWhiteoutHidesFile(t *testing.T) {
	// The layer that deleted /data. It holds no data/keep.txt at all.
	layer := writeTar(t, map[string]string{".wh.data": ""})

	res, link, deleted, err := scanLayerTar(layer, "data/keep.txt", whiteoutsFor("data/keep.txt"))
	if err != nil {
		t.Fatalf("scanLayerTar: %v", err)
	}
	if res != nil || link != "" {
		t.Errorf("got res=%v link=%q, want neither", res, link)
	}
	if !deleted {
		t.Error("ancestor whiteout .wh.data did not hide data/keep.txt")
	}
}

func TestScanLayerTar_SameLayerRecreateWins(t *testing.T) {
	// rm -rf /data && mkdir /data && echo ... > /data/keep.txt in ONE layer:
	// the file is present and must win over the whiteout beside it.
	layer := writeTar(t, map[string]string{
		".wh.data":      "",
		"data/keep.txt": "fresh",
	})

	res, _, deleted, err := scanLayerTar(layer, "data/keep.txt", whiteoutsFor("data/keep.txt"))
	if err != nil {
		t.Fatalf("scanLayerTar: %v", err)
	}
	if deleted {
		t.Fatal("a file recreated in the same layer was reported deleted")
	}
	if res == nil || string(res.Data) != "fresh" {
		t.Fatalf("got %v, want the recreated contents", res)
	}
}

// TestScanLayerTar_NotATarIsAnError covers the other half of the same class of
// lie: a layer stored compressed (what `copy` from a registry produces) failed
// to parse and was reported as "file not found in image", so every path in such
// an image answered confidently and wrongly.
func TestScanLayerTar_NotATarIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notatar")
	if err := os.WriteFile(p, []byte("\x1f\x8b\x08 this is gzip, not a tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := scanLayerTar(p, "etc/os-release", whiteoutsFor("etc/os-release")); err == nil {
		t.Error("a layer that is not a raw tar was reported as 'not found' instead of erroring")
	}
}
