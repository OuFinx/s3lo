package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/OuFinx/s3lo/pkg/chunkstore"
	"github.com/OuFinx/s3lo/pkg/storage"
)

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, name, ref string) {
	ctx := r.Context()
	var manifestData []byte
	var err error

	if strings.HasPrefix(ref, "sha256:") {
		manifestData, err = s.findManifestByDigest(ctx, name, ref)
	} else {
		key := "manifests/" + name + "/" + ref + "/manifest.json"
		manifestData, err = s.Client.GetObject(ctx, s.Bucket, key)
	}

	if err != nil {
		if storage.IsNotFound(err) {
			writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
			return
		}
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	h := sha256.Sum256(manifestData)
	dgst := "sha256:" + hex.EncodeToString(h[:])
	ct := mediaTypeFromManifest(manifestData)

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifestData)))
	w.Header().Set("Docker-Content-Digest", dgst)

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(manifestData)
}

func (s *Server) findManifestByDigest(ctx context.Context, name, digest string) ([]byte, error) {
	prefix := "manifests/" + name + "/"
	keys, err := s.Client.ListKeys(ctx, s.Bucket, prefix)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
		data, err := s.Client.GetObject(ctx, s.Bucket, key)
		if err != nil {
			if storage.IsNotFound(err) {
				continue // key was listed but disappeared (race) — skip
			}
			return nil, err
		}
		h := sha256.Sum256(data)
		if "sha256:"+hex.EncodeToString(h[:]) == digest {
			return data, nil
		}
		if childDigest := indexChildDigest(data, digest); childDigest != "" {
			childData, err := s.Client.GetObject(ctx, s.Bucket, "blobs/sha256/"+childDigest)
			if err != nil {
				if storage.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			return childData, nil
		}
	}
	return nil, fmt.Errorf("object not found: no manifest matching digest %s", digest)
}

func indexChildDigest(data []byte, digest string) string {
	var idx struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return ""
	}
	for _, desc := range idx.Manifests {
		if desc.Digest == digest {
			return strings.TrimPrefix(desc.Digest, "sha256:")
		}
	}
	return ""
}

func mediaTypeFromManifest(data []byte) string {
	var m struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(data, &m); err == nil && m.MediaType != "" {
		return m.MediaType
	}
	return "application/vnd.oci.image.manifest.v1+json"
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, digest string) {
	ctx := r.Context()

	if !strings.HasPrefix(digest, "sha256:") {
		writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
		return
	}
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	key := "blobs/sha256/" + hexDigest

	exists, err := s.Client.HeadObjectExists(ctx, s.Bucket, key)
	if err != nil {
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error")
		return
	}
	if !exists {
		// A chunked layer has no single object to redirect to, so the only way to
		// serve it is to assemble it here, in the data path.
		recipe, chunked, rErr := chunkstore.LoadRecipe(ctx, s.Client, s.Bucket, hexDigest)
		if rErr != nil {
			writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error")
			return
		}
		if !chunked {
			writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
			return
		}
		s.serveChunkedBlob(w, r, digest, recipe)
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Presigned redirect for S3/S3-compatible backends.
	if p, ok := s.Client.(Presigner); ok {
		url, err := p.PresignGetObject(ctx, s.Bucket, key, s.PresignTTL)
		if err == nil {
			http.Redirect(w, r, url, http.StatusSeeOther)
			return
		}
		// Fall through to streaming on presign error.
	}

	// Streaming fallback for GCS, Azure, local.
	// Note: blobs are loaded into memory here. For large blobs on non-S3 backends,
	// use s3lo pull instead of s3lo serve for better performance.
	data, err := s.Client.GetObject(ctx, s.Bucket, key)
	if err != nil {
		if storage.IsNotFound(err) {
			writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
			return
		}
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// serveChunkedBlob streams a layer assembled from chunks. Content-Length comes
// from the recipe, so the client still sees an ordinary blob response; the fact
// that it never existed as one object is invisible to it.
func (s *Server) serveChunkedBlob(w http.ResponseWriter, r *http.Request, digest string, recipe chunkstore.Recipe) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", recipe.Size))
	w.Header().Set("Docker-Content-Digest", digest)

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := chunkstore.Stream(r.Context(), s.Client, s.Bucket, recipe, w); err != nil {
		// The status line is already on the wire by now, so the only honest signal
		// left is to cut the response short of Content-Length; the client will
		// report a truncated body rather than accept a corrupt layer.
		slog.Error("stream chunked blob", "digest", digest, "error", err)
		return
	}
}
