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

// Parse parses an image reference like "s3://my-bucket/myapp:v1.0", "gs://my-bucket/myapp:v1.0",
// "az://my-container/myapp:v1.0", or "local://./store/myapp:v1.0".
func Parse(raw string) (Reference, error) {
	var scheme, rest string
	switch {
	case strings.HasPrefix(raw, "s3://"):
		scheme = "s3"
		rest = strings.TrimPrefix(raw, "s3://")
	case strings.HasPrefix(raw, "gs://"):
		scheme = "gs"
		rest = strings.TrimPrefix(raw, "gs://")
	case strings.HasPrefix(raw, "az://"):
		scheme = "az"
		rest = strings.TrimPrefix(raw, "az://")
	case strings.HasPrefix(raw, "local://"):
		scheme = "local"
		rest = strings.TrimPrefix(raw, "local://")
	default:
		return Reference{}, fmt.Errorf("invalid reference %q: must start with s3://, gs://, az://, or local://", raw)
	}

	bucket, imageAndTag, err := splitBucketPath(scheme, rest)
	if err != nil {
		return Reference{}, fmt.Errorf("invalid reference %q: %w", raw, err)
	}
	if bucket == "" {
		return Reference{}, fmt.Errorf("invalid reference %q: empty bucket", raw)
	}
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

// splitBucketPath splits a path (after the scheme://) into (bucket, imageAndTag).
// For local:// refs, relative prefixes like "./" and "../" are consumed as part
// of the bucket name so that "local://./store/img:tag" gives bucket="./store".
// For s3://, the first slash is always the bucket boundary.
func splitBucketPath(scheme, rest string) (bucket, remainder string, err error) {
	if scheme == "local" {
		switch {
		case strings.HasPrefix(rest, "./"), strings.HasPrefix(rest, "../"):
			// Find the slash that ends the first directory component after the prefix.
			after := rest[strings.Index(rest, "/")+1:]
			i := strings.Index(after, "/")
			if i < 0 {
				return "", "", fmt.Errorf("missing image path after storage root %q", rest)
			}
			boundary := len(rest) - len(after) + i
			return rest[:boundary], rest[boundary+1:], nil
		}
	}
	i := strings.Index(rest, "/")
	if i < 0 {
		return "", "", fmt.Errorf("missing image path")
	}
	return rest[:i], rest[i+1:], nil
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
