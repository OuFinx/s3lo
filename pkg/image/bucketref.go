package image

import (
	"fmt"
	"strings"
)

// ParseBucketRef parses "s3://bucket/", "gs://bucket/", "az://container/", "local://path/", etc.
// into (bucket, prefix). For local:// refs with relative paths (./dir or ../dir), the full relative
// path is used as the bucket so that "local://./store/" gives bucket="./store".
func ParseBucketRef(s3Ref string) (bucket, prefix string, err error) {
	var isLocal bool
	var rest string
	switch {
	case strings.HasPrefix(s3Ref, "s3://"):
		rest = strings.TrimPrefix(s3Ref, "s3://")
	case strings.HasPrefix(s3Ref, "gs://"):
		rest = strings.TrimPrefix(s3Ref, "gs://")
	case strings.HasPrefix(s3Ref, "az://"):
		rest = strings.TrimPrefix(s3Ref, "az://")
	case strings.HasPrefix(s3Ref, "local://"):
		rest = strings.TrimPrefix(s3Ref, "local://")
		isLocal = true
	default:
		return "", "", fmt.Errorf("invalid reference %q: must start with s3://, gs://, az://, or local://", s3Ref)
	}

	if isLocal && (strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, "../")) {
		// Consume the relative prefix + first directory component as the bucket.
		firstSlash := strings.Index(rest, "/")                   // slash in "./"
		after := rest[firstSlash+1:]                             // e.g. "store/" or "store/prefix/"
		secondSlash := strings.Index(after, "/")
		if secondSlash < 0 {
			// e.g. "local://./store" with no trailing slash → whole thing is bucket
			return rest, "", nil
		}
		bucket = rest[:firstSlash+1+secondSlash] // e.g. "./store"
		prefix = after[secondSlash+1:]
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		return bucket, prefix, nil
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
