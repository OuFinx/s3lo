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

	"github.com/OuFinx/s3lo/v2/pkg/chunkstore"
	"github.com/OuFinx/s3lo/v2/pkg/storage"
)

// isNoSuchBucket reports whether err is an object-storage "bucket does not exist"
// error. A missing bucket means the repository namespace does not exist, which
// OCI clients should see as a definitive 404 rather than a retryable 5xx.
func isNoSuchBucket(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NoSuchBucket")
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, bucket, name, ref string) {
	ctx := r.Context()

	release, err := s.acquire(ctx)
	if err != nil {
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "server busy")
		return
	}
	defer release()

	cacheKey := bucket + "/" + name + "/" + ref
	if s.Cache != nil {
		if data, ok := s.Cache.Manifest(cacheKey); ok {
			s.observeManifest("cache")
			s.writeManifest(w, r, bucket, name, ref, data)
			return
		}
	}

	var manifestData []byte
	if strings.HasPrefix(ref, "sha256:") {
		manifestData, err = s.findManifestByDigest(ctx, bucket, name, ref)
	} else {
		s.observeStorage("manifest_get")
		key := "manifests/" + name + "/" + ref + "/manifest.json"
		manifestData, err = s.Client.GetObject(ctx, bucket, key)
	}

	if err != nil {
		s.observeManifest("error")
		if storage.IsNotFound(err) {
			writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
			return
		}
		if isNoSuchBucket(err) {
			writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known")
			return
		}
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	if s.Cache != nil {
		s.Cache.PutManifest(cacheKey, manifestData)
	}
	s.observeManifest("storage")
	s.writeManifest(w, r, bucket, name, ref, manifestData)
}

// writeManifest verifies the manifest (when a Verifier is configured) and emits
// the response. Verification happens here rather than at fetch time so a cached
// manifest is checked on every request, not only on the one that filled the cache.
func (s *Server) writeManifest(w http.ResponseWriter, r *http.Request, bucket, name, ref string, manifestData []byte) {
	h := sha256.Sum256(manifestData)
	dgst := "sha256:" + hex.EncodeToString(h[:])

	if s.Verifier != nil {
		// Signatures are keyed by tag, so a pull by bare digest has no signature
		// file to read. Such a pull is only allowed when this process already
		// verified the same content through its tag.
		var err error
		if strings.HasPrefix(ref, "sha256:") {
			if !s.Verifier.IsVerified(dgst) {
				err = ErrNotSigned
			}
		} else {
			err = s.Verifier.Check(r.Context(), s.Client, bucket, name, ref, manifestData)
		}
		if err != nil {
			slog.Warn("refusing manifest that failed verification", "image", name, "ref", ref, "error", err)
			writeOCIError(w, http.StatusForbidden, "DENIED", "image signature verification failed")
			return
		}
	}

	w.Header().Set("Content-Type", mediaTypeFromManifest(manifestData))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifestData)))
	w.Header().Set("Docker-Content-Digest", dgst)

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(manifestData)
}

func (s *Server) findManifestByDigest(ctx context.Context, bucket, name, digest string) ([]byte, error) {
	prefix := "manifests/" + name + "/"
	s.observeStorage("manifest_list")
	keys, err := s.Client.ListKeys(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if !strings.HasSuffix(key, "/manifest.json") {
			continue
		}
		s.observeStorage("manifest_get")
		data, err := s.Client.GetObject(ctx, bucket, key)
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
			// Platform manifests are stored as plain blobs. They sit far below one
			// chunk, so they are never held as a recipe.
			s.observeStorage("blob_get")
			childData, err := s.Client.GetObject(ctx, bucket, "blobs/sha256/"+childDigest)
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

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, bucket, digest string) {
	ctx := r.Context()

	if !strings.HasPrefix(digest, "sha256:") {
		s.observeBlob("error")
		writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
		return
	}
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	key := "blobs/sha256/" + hexDigest

	release, err := s.acquire(ctx)
	if err != nil {
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "server busy")
		return
	}
	defer release()

	s.observeStorage("blob_head")
	exists, err := s.Client.HeadObjectExists(ctx, bucket, key)
	if err != nil {
		s.observeBlob("error")
		if isNoSuchBucket(err) {
			writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known")
			return
		}
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error")
		return
	}
	if !exists {
		// A chunked layer has no single object to redirect to, so the only way to
		// serve it is to assemble it here, in the data path.
		recipe, chunked, rErr := chunkstore.LoadRecipe(ctx, s.Client, bucket, hexDigest)
		if rErr != nil {
			s.observeBlob("error")
			writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error")
			return
		}
		if !chunked {
			s.observeBlob("error")
			writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
			return
		}
		if recipe.CompressedDigest == "" || recipe.CompressedSize == 0 {
			// A recipe without a compressed identity cannot be served: its
			// Content-Length would be zero and the body would be cut short, which
			// a client reports as a corrupt layer rather than a server error.
			slog.Error("recipe has no compressed identity", "digest", digest)
			s.observeBlob("error")
			writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"layer recipe is missing its compressed form")
			return
		}
		s.serveChunkedBlob(w, r, bucket, digest, recipe)
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
		return
	}

	// A presigned redirect keeps blob bytes out of this process entirely, which is
	// the cheapest way to serve a whole-layer blob.
	if p, ok := s.Client.(Presigner); ok {
		s.observeStorage("blob_presign")
		url, err := p.PresignGetObject(ctx, bucket, key, s.PresignTTL)
		if err == nil {
			s.observeBlob("redirect")
			http.Redirect(w, r, url, http.StatusSeeOther)
			return
		}
		// Fall through to streaming on presign error.
	}

	// Streaming fallback for GCS, Azure, local.
	// Note: blobs are loaded into memory here. For large blobs on non-S3 backends,
	// use s3lo pull instead of s3lo serve for better performance.
	s.observeStorage("blob_get")
	data, err := s.Client.GetObject(ctx, bucket, key)
	if err != nil {
		s.observeBlob("error")
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
	s.observeBlob("stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// serveChunkedBlob streams a layer assembled from chunks. Content-Length comes
// from the recipe, so the client still sees an ordinary blob response; that it
// never existed as one object is invisible to it.
//
// The chunk objects are shipped exactly as stored, which makes the response a
// valid zstd stream matching the digest the manifest advertises. Nothing is
// decompressed here: the client decompresses, as it would for any registry, and
// roughly a third of the bytes move.
func (s *Server) serveChunkedBlob(w http.ResponseWriter, r *http.Request, bucket, digest string, recipe chunkstore.Recipe) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", recipe.CompressedSize))
	w.Header().Set("Docker-Content-Digest", digest)

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.observeBlob("chunked")
	w.WriteHeader(http.StatusOK)
	if err := chunkstore.StreamCompressed(r.Context(), s.Client, bucket, recipe, w); err != nil {
		// The status line is already on the wire by now, so the only honest signal
		// left is to cut the response short of Content-Length; the client reports a
		// truncated body rather than accepting a corrupt layer.
		slog.Error("stream chunked blob", "digest", digest, "error", err)
		return
	}
}
