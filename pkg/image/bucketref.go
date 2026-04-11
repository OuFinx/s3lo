package image

import (
	"fmt"
	"strings"
)

// ParseBucketRef parses "s3://bucket" or "s3://bucket/prefix/" into bucket + prefix.
func ParseBucketRef(s3Ref string) (bucket, prefix string, err error) {
	if !strings.HasPrefix(s3Ref, "s3://") {
		return "", "", fmt.Errorf("invalid s3 reference %q: must start with s3://", s3Ref)
	}
	rest := strings.TrimPrefix(s3Ref, "s3://")
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return rest, "", nil
	}
	bucket = rest[:slashIdx]
	prefix = rest[slashIdx+1:]
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return bucket, prefix, nil
}
