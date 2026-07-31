package ref

import (
	"fmt"
	"regexp"
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
		if !digestPattern.MatchString(ref.Digest) {
			return Reference{}, fmt.Errorf("invalid reference %q: malformed digest %q (want algo:hex, e.g. sha256:<64 hex chars>)", raw, ref.Digest)
		}
		if err := validateImage(ref.Image); err != nil {
			return Reference{}, fmt.Errorf("invalid reference %q: %w", raw, err)
		}
		return ref, nil
	}

	// Check for tag (:tag)
	if colonIdx := strings.LastIndex(imageAndTag, ":"); colonIdx >= 0 {
		ref.Image = imageAndTag[:colonIdx]
		ref.Tag = imageAndTag[colonIdx+1:]
		if ref.Tag == "" {
			return Reference{}, fmt.Errorf("invalid reference %q: empty tag after ':'", raw)
		}
	} else {
		ref.Image = imageAndTag
		ref.Tag = "latest"
	}

	if err := validateImage(ref.Image); err != nil {
		return Reference{}, fmt.Errorf("invalid reference %q: %w", raw, err)
	}
	if !safeSegment(ref.Tag) {
		return Reference{}, fmt.Errorf("invalid reference %q: illegal tag %q (a tag must be a single path segment and cannot be \".\" or \"..\")", raw, ref.Tag)
	}

	return ref, nil
}

// digestPattern matches an OCI content digest: a lowercase algorithm followed
// by its hex encoding. Anything else is rejected at parse time so that no
// caller can splice an unvalidated string into a storage key or a file path.
var digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[a-fA-F0-9]{32,128}$`)

// safeSegment reports whether s can be used as one path segment of a storage
// key. Empty, "." and ".." are rejected because every backend joins these into
// a key prefix, and the local backend resolves them against the filesystem.
func safeSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\")
}

// validateImage checks every component of a (possibly nested) image name.
// Without this, a tag or image component of ".." escapes the manifests prefix:
// "s3lo delete local://./store/app:.." deleted every image in the store,
// bypassing the immutability check, and reported success.
func validateImage(image string) error {
	if image == "" {
		return fmt.Errorf("empty image name")
	}
	for _, part := range strings.Split(image, "/") {
		if !safeSegment(part) {
			return fmt.Errorf("illegal image name %q: component %q is not a valid path segment", image, part)
		}
	}
	return nil
}

// splitBucketPath splits a path (after the scheme://) into (bucket, imageAndTag).
// For local:// refs the storage root is a SINGLE leading path component. Rooted
// prefixes ("./", "../", or an absolute "/") are consumed together with the first
// directory component, so "local://./store/img:tag", "local://../store/img:tag"
// and "local:///store/img:tag" all give bucket="<root>", image="img". The image
// may itself contain slashes ("org/app").
//
// Note: because the root is a single component, a deep root is NOT supported —
// "local:///var/lib/store/img" parses bucket="/var", image="lib/store/img", the
// same one-level limitation the relative form has. Use a single-level root (e.g.
// bind-mount or symlink your storage dir) if you need an absolute path.
//
// For s3://, gs:// and az:// the first slash is always the bucket boundary.
func splitBucketPath(scheme, rest string) (bucket, remainder string, err error) {
	if scheme == "local" {
		prefixLen := 0
		switch {
		case strings.HasPrefix(rest, "./"):
			prefixLen = 2
		case strings.HasPrefix(rest, "../"):
			prefixLen = 3
		case strings.HasPrefix(rest, "/"):
			prefixLen = 1
		}
		if prefixLen > 0 {
			// Find the slash that ends the first directory component after the prefix.
			i := strings.Index(rest[prefixLen:], "/")
			if i < 0 {
				return "", "", fmt.Errorf("missing image path after storage root %q", rest)
			}
			boundary := prefixLen + i
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

// SigningPayload returns the canonical bytes covered by an image signature.
//
// The payload binds the signature to the exact location it was made for, so a
// signature cannot be transplanted onto a different tag, image or bucket. Both
// the signer and every verifier build these bytes from the reference under
// inspection; nothing is ever read back out of the stored signature record.
//
// Before this binding existed the payload was the manifest digest alone, which
// made a valid signature freely portable: an attacker with bucket write access
// could copy a signed staging manifest and its signature onto a production tag,
// or roll a tag back to an older signed release, and verification still passed.
func SigningPayload(bucket, image, tag, digest string) []byte {
	return []byte("s3lo-signature-v2\n" + bucket + "/" + image + ":" + tag + "\n" + digest + "\n")
}
