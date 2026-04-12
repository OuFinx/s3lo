package image

import (
	"fmt"
	"strings"
)

// ParseBucketRef parses "s3://bucket", "local://path", or their prefixed variants into bucket + prefix.
func ParseBucketRef(s3Ref string) (bucket, prefix string, err error) {
	var rest string
	switch {
	case strings.HasPrefix(s3Ref, "s3://"):
		rest = strings.TrimPrefix(s3Ref, "s3://")
	case strings.HasPrefix(s3Ref, "local://"):
		rest = strings.TrimPrefix(s3Ref, "local://")
	default:
		return "", "", fmt.Errorf("invalid reference %q: must start with s3:// or local://", s3Ref)
	}
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
