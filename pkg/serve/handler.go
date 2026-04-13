package serve

import "net/http"

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, name, ref string) {
	writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
	_, _ = name, ref
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, digest string) {
	writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
	_ = digest
}
