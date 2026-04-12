package image

import (
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// parseOCIRef parses an OCI reference into registry, image, and tag.
// Supports all standard forms:
//
//	alpine                               → docker.io / library/alpine / latest
//	alpine:3.18                          → docker.io / library/alpine / 3.18
//	nginx:latest                         → docker.io / library/nginx  / latest
//	user/myapp:v1.0                      → docker.io / user/myapp     / v1.0
//	docker.io/library/alpine:latest      → docker.io / library/alpine / latest
//	ghcr.io/owner/image:tag              → ghcr.io   / owner/image    / tag
//	123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1  → ECR registry
func parseOCIRef(raw string) (registry, image, tag string, err error) {
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")

	parts := strings.SplitN(raw, "/", 2)

	// Determine whether the first component is a registry hostname.
	// A hostname contains a dot or colon, or is "localhost".
	isRegistry := len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost")

	var rest string
	if isRegistry {
		registry = parts[0]
		rest = parts[1]
	} else {
		// No registry prefix — default to Docker Hub.
		registry = "registry-1.docker.io"
		rest = raw
		// Official images (no slash) get the "library/" namespace.
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
	}

	// Split image name and tag on the last colon.
	if colonIdx := strings.LastIndex(rest, ":"); colonIdx >= 0 {
		image = rest[:colonIdx]
		tag = rest[colonIdx+1:]
	} else {
		image = rest
		tag = "latest"
	}

	if image == "" {
		return "", "", "", fmt.Errorf("invalid OCI reference %q: could not parse image name", raw)
	}
	return registry, image, tag, nil
}

// indexPlatformList returns a human-readable comma-separated list of platforms in an index.
func indexPlatformList(idx ocispec.Index) string {
	var parts []string
	for _, d := range idx.Manifests {
		parts = append(parts, platformString(d.Platform))
	}
	return strings.Join(parts, ", ")
}
