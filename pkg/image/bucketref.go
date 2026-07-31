package image

import (
	"fmt"
	"strings"
)

// ParseBucketRef parses "s3://bucket/", "gs://bucket/", "az://container/", "local://path/", etc.
// into (bucket, nameFilter). For local:// refs with relative paths (./dir or ../dir), the full
// relative path is used as the bucket so that "local://./store/" gives bucket="./store".
//
// nameFilter is an image-name filter, not a key prefix. Nothing writes objects
// under it: the write path folds everything after the bucket into the image
// name, so "s3://bucket/team/" selects the images called team/* whose manifests
// live at manifests/team/... and whose blobs live at the bucket root like
// everyone else's. Use scopedManifests to turn it into a listing prefix.
func ParseBucketRef(s3Ref string) (bucket, nameFilter string, err error) {
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
		firstSlash := strings.Index(rest, "/") // slash in "./"
		after := rest[firstSlash+1:]           // e.g. "store/" or "store/prefix/"
		secondSlash := strings.Index(after, "/")
		if secondSlash < 0 {
			// e.g. "local://./store" with no trailing slash → whole thing is bucket
			return rest, "", nil
		}
		bucket = rest[:firstSlash+1+secondSlash] // e.g. "./store"
		nameFilter = after[secondSlash+1:]
		if nameFilter != "" && !strings.HasSuffix(nameFilter, "/") {
			nameFilter += "/"
		}
		return bucket, nameFilter, nil
	}

	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return rest, "", nil
	}
	bucket = rest[:slashIdx]
	nameFilter = rest[slashIdx+1:]
	if nameFilter != "" && !strings.HasSuffix(nameFilter, "/") {
		nameFilter += "/"
	}
	return bucket, nameFilter, nil
}

// manifestsRoot is where every writer puts manifests: at the bucket root, never
// under the trailing path of the ref that addressed them.
const manifestsRoot = "manifests/"

// scopedManifests turns a bucket ref's image-name filter into a listing prefix
// for manifests.
//
// Only manifests are ever narrowed. Blobs, recipes, indexes and chunks are
// shared by every image in the bucket, which is the whole point of the layout,
// so scoping them would under-report storage and, worse, let GC free a blob
// another team's image still references.
func scopedManifests(nameFilter string) string { return manifestsRoot + nameFilter }
