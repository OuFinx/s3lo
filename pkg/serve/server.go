package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/v2/pkg/storage"
)

// Observer receives one call per served request so a deployment can export
// metrics without this package depending on any metrics library. Leave the
// field nil to record nothing; the CLI does, the operator supplies a Prometheus
// implementation.
type Observer interface {
	// ManifestServed reports source as "cache", "storage" or "error".
	ManifestServed(source string)
	// BlobServed reports status as "redirect", "stream", "chunked" or "error".
	BlobServed(status string)
	// StorageCall reports an outbound object-storage operation by name.
	StorageCall(op string)
}

// Server implements http.Handler for the read half of the OCI Distribution Spec.
//
// It has two addressing modes. With Bucket set it serves that one bucket and
// paths look like /v2/<image>/manifests/<ref>, which is what `s3lo serve` wants.
// With Bucket empty the first path segment names the bucket
// (/v2/<bucket>/<image>/manifests/<ref>), which is what a node-local proxy wants
// when pods reference images as <host>/<bucket>/<image>:<tag>.
type Server struct {
	Client     storage.Backend
	Bucket     string
	PresignTTL time.Duration

	// Cache is optional. Nil disables caching entirely.
	Cache *Cache
	// Verifier is optional. When set, a manifest is only served if it carries a
	// valid signature, so an unsigned image cannot reach a node.
	Verifier *Verifier
	// Observer is optional.
	Observer Observer
	// MaxConcurrentStorage bounds in-flight object-storage operations. Zero means
	// unlimited.
	MaxConcurrentStorage int
	// HealthBucket, when set, makes /readyz probe object storage instead of
	// answering unconditionally.
	HealthBucket string

	sem chan struct{}
}

// ErrBadPath is returned when a request path does not name a bucket in
// multi-bucket mode.
var ErrBadPath = errors.New("serve: request path does not name a bucket")

func (s *Server) observeManifest(source string) {
	if s.Observer != nil {
		s.Observer.ManifestServed(source)
	}
}

func (s *Server) observeBlob(status string) {
	if s.Observer != nil {
		s.Observer.BlobServed(status)
	}
}

func (s *Server) observeStorage(op string) {
	if s.Observer != nil {
		s.Observer.StorageCall(op)
	}
}

// acquire bounds concurrent storage work. It returns a release func that is
// always safe to call.
func (s *Server) acquire(ctx context.Context) (func(), error) {
	if s.MaxConcurrentStorage <= 0 {
		return func() {}, nil
	}
	if s.sem == nil {
		// Built lazily so a zero-value Server stays usable; callers that need a
		// pre-sized limiter should use NewServer.
		s.sem = make(chan struct{}, s.MaxConcurrentStorage)
	}
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}

// NewServer returns a Server with its internal limiter sized up front, which is
// what long-running deployments should use.
func NewServer(s Server) *Server {
	if s.MaxConcurrentStorage > 0 {
		s.sem = make(chan struct{}, s.MaxConcurrentStorage)
	}
	return &s
}

// resolve splits the path remainder after /v2/ into the bucket to read from and
// the remaining reference path.
func (s *Server) resolve(rest string) (bucket, remainder string, err error) {
	if s.Bucket != "" {
		return s.Bucket, rest, nil
	}
	i := strings.Index(rest, "/")
	if i <= 0 {
		return "", "", ErrBadPath
	}
	return rest[:i], rest[i+1:], nil
}

// ServeHTTP dispatches OCI Distribution Spec requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	case "/readyz":
		s.handleReady(w, r)
		return
	}

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

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// The write half of the spec is deliberately absent: images are published
		// with `s3lo push`, not through this endpoint.
		writeOCIError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "read-only registry")
		return
	}

	rest := strings.TrimPrefix(path, "/v2/")

	if i := strings.LastIndex(rest, "/manifests/"); i >= 0 {
		bucket, name, err := s.resolve(rest[:i])
		if err != nil {
			writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name must include a bucket")
			return
		}
		s.handleManifest(w, r, bucket, name, rest[i+len("/manifests/"):])
		return
	}
	if i := strings.LastIndex(rest, "/blobs/"); i >= 0 {
		bucket, _, err := s.resolve(rest[:i])
		if err != nil {
			writeOCIError(w, http.StatusNotFound, "NAME_UNKNOWN", "repository name must include a bucket")
			return
		}
		s.handleBlob(w, r, bucket, rest[i+len("/blobs/"):])
		return
	}

	writeOCIError(w, http.StatusNotFound, "UNSUPPORTED", "unsupported endpoint")
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.HealthBucket == "" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// The key need not exist: reachability and credentials are what is being
	// probed, and a 404 answers both.
	if _, err := s.Client.HeadObjectExists(ctx, s.HealthBucket, "s3lo-health-check"); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("storage unreachable"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
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
