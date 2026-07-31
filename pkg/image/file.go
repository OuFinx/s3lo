package image

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/OuFinx/s3lo/v3/pkg/chunkstore"
	"github.com/OuFinx/s3lo/v3/pkg/oci"
	"github.com/OuFinx/s3lo/v3/pkg/ref"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
)

// ErrFileNotInImage is returned when no layer holds the requested path, or a
// layer above the one that does has deleted it.
var ErrFileNotInImage = errors.New("file not found in image")

// CatResult carries the file's bytes and what reading them cost.
type CatResult struct {
	Data []byte
	// Layer is the digest of the layer the file came from, as the manifest
	// advertises it.
	Layer string
	// Indexed is false when the layer had no file index and the whole thing had
	// to be downloaded to find one file.
	Indexed bool
	// ChunksFetched and ChunksTotal are meaningful only when Indexed.
	ChunksFetched, ChunksTotal int
}

// CatFile reads one file out of an image without materialising the image.
//
// On an indexed layer it fetches only the chunks the file's bytes fall in, which
// is the thing a registry cannot do: the OCI Distribution Spec addresses whole
// layers, so the same read there means downloading the layer.
//
// Layers are searched from the top down and whiteouts are honoured, so the
// answer is what a container would actually see at that path.
func CatFile(ctx context.Context, s3Ref, filePath, platform string) (*CatResult, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid S3 reference: %w", err)
	}
	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	manifestData, err := client.GetObject(ctx, parsed.Bucket, parsed.ManifestsPrefix()+tagManifestFile)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	if isImageIndex(manifestData) {
		manifestData, err = resolvePlatformManifest(ctx, client, parsed.Bucket, manifestData, platform)
		if err != nil {
			return nil, err
		}
	}
	manifest, err := oci.ParseManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	want := chunkstore.NormalisePath(filePath)
	for hop := 0; ; hop++ {
		if hop == maxLinkHops {
			return nil, fmt.Errorf("%s: too many links followed, giving up at %s", filePath, want)
		}
		whiteouts := whiteoutsFor(want)

		// Top layer wins, and a whiteout above stops the search: that is what
		// the running container sees.
		var (
			res     *CatResult
			link    string
			deleted bool
		)
		for i := len(manifest.Layers) - 1; i >= 0 && res == nil && link == "" && !deleted; i-- {
			d := manifest.Layers[i].Digest.Encoded()
			res, link, deleted, err = catFromLayer(ctx, client, parsed.Bucket, d, want, whiteouts)
			if err != nil {
				return nil, fmt.Errorf("read layer %s: %w", short(d), err)
			}
			if res != nil {
				res.Layer = d
			}
		}
		switch {
		case deleted:
			return nil, fmt.Errorf("%w: %s was deleted by a later layer", ErrFileNotInImage, filePath)
		case link != "":
			want = resolveLink(want, link)
		case res != nil:
			return res, nil
		default:
			return nil, fmt.Errorf("%w: %s", ErrFileNotInImage, filePath)
		}
	}
}

// maxLinkHops bounds symlink following, so a loop in the image is an error
// rather than a hang.
const maxLinkHops = 16

// resolveLink turns a link target into a path in the image. A relative target is
// resolved against the directory holding the link, which is why "../usr/lib/..."
// from /etc/os-release lands on /usr/lib/os-release.
func resolveLink(from, target string) string {
	if strings.HasPrefix(target, "/") {
		return chunkstore.NormalisePath(target)
	}
	return chunkstore.NormalisePath(path.Join(path.Dir(from), target))
}

// whiteoutsFor returns every marker whose presence in a layer means p is gone
// from the layers beneath it.
//
// A deletion is recorded beside what was deleted, and deleting a directory
// deletes everything under it: "RUN rm -rf /data" emits a single ".wh.data"
// entry, not one marker per file below it. Checking only the file's own marker
// meant `s3lo cat` printed the contents of a file the running container reports
// as missing, and exited 0 — a wrong answer given confidently.
//
// Opaque markers (".wh..wh..opq") hide the whole of a directory's lower-layer
// content and are included for the same reason.
func whiteoutsFor(p string) []string {
	dir, base := path.Split(p)
	out := []string{dir + ".wh." + base}
	for d := path.Clean(dir); d != "." && d != "/" && d != ""; d = path.Dir(d) {
		pd, pb := path.Split(d)
		out = append(out, pd+".wh."+pb, d+"/.wh..wh..opq")
	}
	return out
}

// isWhiteout reports whether name is one of the markers in whiteouts.
func isWhiteout(whiteouts []string, name string) bool {
	for _, w := range whiteouts {
		if w == name {
			return true
		}
	}
	return false
}

// catFromLayer looks for want inside one layer. It reports deleted=true when the
// layer holds a whiteout for the path, a non-empty link when the path is a link
// the caller must follow, and a nil result when the layer does not mention it.
func catFromLayer(ctx context.Context, client storage.Backend, bucket, layerDigest, want string, whiteouts []string) (*CatResult, string, bool, error) {
	recipe, chunked, err := chunkstore.LoadRecipe(ctx, client, bucket, layerDigest)
	if err != nil {
		return nil, "", false, err
	}
	if chunked {
		ix, ok, err := chunkstore.LoadIndex(ctx, client, bucket, layerDigest)
		if err != nil {
			return nil, "", false, err
		}
		if ok {
			// The file is looked for before its whiteouts: a layer that removes a
			// directory and recreates a file inside it holds both, and what the
			// layer put there is what the container sees.
			entry, found := ix.Find(want)
			if !found {
				for _, w := range whiteouts {
					if _, hit := ix.Find(w); hit {
						return nil, "", true, nil
					}
				}
				return nil, "", false, nil
			}
			if entry.Link != "" {
				return nil, entry.Link, false, nil
			}
			data, err := chunkstore.ExtractFile(ctx, client, bucket, recipe, entry)
			if err != nil {
				return nil, "", false, err
			}
			need, total := chunkstore.ChunksFor(recipe, entry)
			return &CatResult{Data: data, Indexed: true, ChunksFetched: need, ChunksTotal: total}, "", false, nil
		}
	}

	// No index: the layer predates them or is stored whole. Correct, just not
	// cheap — the whole layer comes down to answer a question about one file.
	dir, err := os.MkdirTemp("", "s3lo-cat-*")
	if err != nil {
		return nil, "", false, err
	}
	defer os.RemoveAll(dir)
	rawDigest, _, err := fetchLayer(ctx, client, bucket, layerDigest, dir)
	if err != nil {
		return nil, "", false, err
	}
	return scanLayerTar(dir+"/"+rawDigest, want, whiteouts)
}

// scanLayerTar is the unindexed fallback: walk the layer's tar looking for the
// path and for anything that whiteouts it.
func scanLayerTar(localPath, want string, whiteouts []string) (*CatResult, string, bool, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, "", false, err
	}
	defer f.Close()

	tr := tar.NewReader(f)
	deleted := false
	first := true
	for {
		h, err := tr.Next()
		if err == io.EOF {
			// A whiteout only counts once the whole layer has been searched, so
			// that a file recreated beside its own marker still wins.
			return nil, "", deleted, nil
		}
		if err != nil {
			if first {
				// Layers copied from a registry are stored gzip-compressed, so
				// there is no tar here to read. Reporting that as "not found"
				// made every path in such an image answer confidently and
				// wrongly; say what is actually wrong instead.
				return nil, "", false, fmt.Errorf("layer is not a raw tar (stored compressed): %w", err)
			}
			return nil, "", false, fmt.Errorf("read layer tar: %w", err)
		}
		first = false
		name := chunkstore.NormalisePath(h.Name)
		if isWhiteout(whiteouts, name) {
			deleted = true
			continue
		}
		if name != want {
			continue
		}
		switch h.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			return nil, h.Linkname, false, nil
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, "", false, fmt.Errorf("read %s: %w", want, err)
			}
			return &CatResult{Data: data}, "", false, nil
		}
	}
}

// Savings describes an indexed read as a ratio, for reporting.
func (r *CatResult) Savings() string {
	if !r.Indexed || r.ChunksTotal == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d chunks", r.ChunksFetched, r.ChunksTotal)
}
