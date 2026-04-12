package image

import (
	"encoding/json"
	"runtime"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	mediaTypeOCIIndex     = "application/vnd.oci.image.index.v1+json"
	mediaTypeDockerList   = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaTypeOCIManifest  = "application/vnd.oci.image.manifest.v1+json"
	mediaTypeDockerV2     = "application/vnd.docker.distribution.manifest.v2+json"
)

// isImageIndex returns true if the manifest data is an OCI Image Index or Docker manifest list.
func isImageIndex(data []byte) bool {
	var probe struct {
		MediaType    string `json:"mediaType"`
		SchemaVersion int   `json:"schemaVersion"`
		Manifests    []json.RawMessage `json:"manifests"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	if probe.MediaType == mediaTypeOCIIndex || probe.MediaType == mediaTypeDockerList {
		return true
	}
	// Some registries omit mediaType but include manifests array — treat as index.
	if probe.MediaType == "" && len(probe.Manifests) > 0 && probe.SchemaVersion == 2 {
		var entry struct {
			Platform *ocispec.Platform `json:"platform"`
		}
		if json.Unmarshal(probe.Manifests[0], &entry) == nil && entry.Platform != nil {
			return true
		}
	}
	return false
}

// parseIndex parses an OCI Image Index or Docker manifest list.
func parseIndex(data []byte) (ocispec.Index, error) {
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return ocispec.Index{}, err
	}
	return idx, nil
}

// hostPlatform returns the host platform string for container image selection (e.g. "linux/amd64").
// Container images are always linux-based, so darwin and windows hosts are normalized to linux —
// matching the behavior of Docker and other OCI tools (a Mac user wants linux/arm64, not darwin/arm64).
func hostPlatform() string {
	goos := runtime.GOOS
	if goos == "darwin" || goos == "windows" {
		goos = "linux"
	}
	arch := runtime.GOARCH
	return goos + "/" + arch
}

// parsePlatform parses a platform string like "linux/amd64" or "linux/arm/v7".
func parsePlatform(s string) (os, arch, variant string) {
	parts := strings.SplitN(s, "/", 3)
	if len(parts) >= 1 {
		os = parts[0]
	}
	if len(parts) >= 2 {
		arch = parts[1]
	}
	if len(parts) >= 3 {
		variant = parts[2]
	}
	return
}

// matchesPlatform returns true if the descriptor's platform matches the given platform string.
func matchesPlatform(desc ocispec.Descriptor, platform string) bool {
	if desc.Platform == nil {
		return false
	}
	wantOS, wantArch, wantVariant := parsePlatform(platform)
	if desc.Platform.OS != wantOS || desc.Platform.Architecture != wantArch {
		return false
	}
	if wantVariant != "" && desc.Platform.Variant != wantVariant {
		return false
	}
	return true
}

// platformString formats an ocispec.Platform as "os/arch[/variant]".
// Attestation manifests (e.g. SLSA provenance) have platform with "unknown" OS
// or nil platform; these are labeled "(attestation)".
func platformString(p *ocispec.Platform) string {
	if p == nil {
		return "(attestation)"
	}
	if p.OS == "" || p.OS == "unknown" {
		return "(attestation)"
	}
	s := p.OS + "/" + p.Architecture
	if p.Variant != "" {
		s += "/" + p.Variant
	}
	return s
}

// IsAttestationPlatform returns true if the platform represents an attestation
// manifest rather than a runnable image.
func IsAttestationPlatform(p string) bool {
	return p == "(attestation)"
}

// acceptHeader returns the Accept header value for manifest requests, including index types.
func acceptHeader() string {
	return strings.Join([]string{
		mediaTypeOCIIndex,
		mediaTypeDockerList,
		mediaTypeOCIManifest,
		mediaTypeDockerV2,
	}, ",")
}
