package ref

import (
	"fmt"
	"strings"
)

// Reference represents a parsed s3://bucket/image:tag or local://path/image:tag reference.
type Reference struct {
	Scheme string
	Bucket string
	Image  string
	Tag    string
	Digest string
}

// Parse parses an S3 or local image reference like "s3://my-bucket/myapp:v1.0" or "local:///tmp/store/myapp:v1.0".
func Parse(raw string) (Reference, error) {
	var scheme, rest string
	switch {
	case strings.HasPrefix(raw, "s3://"):
		scheme = "s3"
		rest = strings.TrimPrefix(raw, "s3://")
	case strings.HasPrefix(raw, "local://"):
		scheme = "local"
		rest = strings.TrimPrefix(raw, "local://")
	default:
		return Reference{}, fmt.Errorf("invalid reference %q: must start with s3:// or local://", raw)
	}

	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return Reference{}, fmt.Errorf("invalid reference %q: missing image path", raw)
	}

	bucket := rest[:slashIdx]
	if bucket == "" {
		return Reference{}, fmt.Errorf("invalid reference %q: empty bucket", raw)
	}

	imageAndTag := rest[slashIdx+1:]
	if imageAndTag == "" || imageAndTag == "/" {
		return Reference{}, fmt.Errorf("invalid reference %q: missing image name", raw)
	}

	ref := Reference{Scheme: scheme, Bucket: bucket}

	// Check for digest (@sha256:...)
	if atIdx := strings.Index(imageAndTag, "@"); atIdx >= 0 {
		ref.Image = imageAndTag[:atIdx]
		ref.Digest = imageAndTag[atIdx+1:]
		return ref, nil
	}

	// Check for tag (:tag)
	if colonIdx := strings.LastIndex(imageAndTag, ":"); colonIdx >= 0 {
		ref.Image = imageAndTag[:colonIdx]
		ref.Tag = imageAndTag[colonIdx+1:]
	} else {
		ref.Image = imageAndTag
		ref.Tag = "latest"
	}

	return ref, nil
}

// S3Prefix returns the S3 key prefix for this image: "image/tag".
func (r Reference) S3Prefix() string {
	tag := r.Tag
	if tag == "" {
		tag = r.Digest
	}
	return r.Image + "/" + tag
}

// ManifestsPrefix returns the v1.1.0 S3 prefix for this image's manifests.
// Example: "manifests/myapp/v1.0/"
func (r Reference) ManifestsPrefix() string {
	tag := r.Tag
	if tag == "" {
		tag = r.Digest
	}
	return "manifests/" + r.Image + "/" + tag + "/"
}

// String returns the canonical reference string.
func (r Reference) String() string {
	scheme := r.Scheme
	if scheme == "" {
		scheme = "s3"
	}
	if r.Digest != "" {
		return fmt.Sprintf("%s://%s/%s@%s", scheme, r.Bucket, r.Image, r.Digest)
	}
	return fmt.Sprintf("%s://%s/%s:%s", scheme, r.Bucket, r.Image, r.Tag)
}
