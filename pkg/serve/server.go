package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/pkg/storage"
)

// Server implements http.Handler for the OCI Distribution Spec.
// It serves images stored in Bucket using Client as the storage backend.
type Server struct {
	Client     storage.Backend
	Bucket     string
	PresignTTL time.Duration
}

// ServeHTTP dispatches OCI Distribution Spec requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// GET /v2/ — OCI version check
	if path == "/v2/" || path == "/v2" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}

	if !strings.HasPrefix(path, "/v2/") {
		writeOCIError(w, http.StatusNotFound, "UNSUPPORTED", "unsupported endpoint")
		return
	}

	rest := strings.TrimPrefix(path, "/v2/")

	if i := strings.LastIndex(rest, "/manifests/"); i >= 0 {
		name := rest[:i]
		ref := rest[i+len("/manifests/"):]
		s.handleManifest(w, r, name, ref)
		return
	}
	if i := strings.LastIndex(rest, "/blobs/"); i >= 0 {
		digest := rest[strings.LastIndex(rest, "/blobs/")+len("/blobs/"):]
		s.handleBlob(w, r, digest)
		return
	}

	writeOCIError(w, http.StatusNotFound, "UNSUPPORTED", "unsupported endpoint")
}

type ociErrorBody struct {
	Errors []ociErrorEntry `json:"errors"`
}

type ociErrorEntry struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeOCIError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ociErrorBody{
		Errors: []ociErrorEntry{{Code: code, Message: msg}},
	})
}
