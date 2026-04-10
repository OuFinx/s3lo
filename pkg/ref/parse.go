package ref

import (
	"fmt"
	"strings"
)

// Reference represents a parsed s3://bucket/image:tag reference.
type Reference struct {
	Bucket string
	Image  string
	Tag    string
	Digest string
}

// Parse parses an S3 image reference like "s3://my-bucket/myapp:v1.0".
func Parse(raw string) (Reference, error) {
	if !strings.HasPrefix(raw, "s3://") {
		return Reference{}, fmt.Errorf("invalid s3 reference %q: must start with s3://", raw)
	}

	rest := strings.TrimPrefix(raw, "s3://")

	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return Reference{}, fmt.Errorf("invalid s3 reference %q: missing image path", raw)
	}

	bucket := rest[:slashIdx]
	if bucket == "" {
		return Reference{}, fmt.Errorf("invalid s3 reference %q: empty bucket", raw)
	}

	imageAndTag := rest[slashIdx+1:]
	if imageAndTag == "" || imageAndTag == "/" {
		return Reference{}, fmt.Errorf("invalid s3 reference %q: missing image name", raw)
	}

	ref := Reference{Bucket: bucket}

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

// String returns the canonical s3:// reference string.
func (r Reference) String() string {
	if r.Digest != "" {
		return fmt.Sprintf("s3://%s/%s@%s", r.Bucket, r.Image, r.Digest)
	}
	return fmt.Sprintf("s3://%s/%s:%s", r.Bucket, r.Image, r.Tag)
}
