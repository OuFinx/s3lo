package image

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	digest "github.com/opencontainers/go-digest"
)

// verifyBytesDigest checks that sha256(data) equals the expected hex digest
// (the digest with any "sha256:" prefix already stripped, i.e. Descriptor.Digest.Encoded()).
// A content-addressable store must validate content against its address on read;
// otherwise a corrupted or tampered blob is trusted blindly.
func verifyBytesDigest(data []byte, wantHex string) error {
	sum := sha256.Sum256(data)
	got := fmt.Sprintf("%x", sum)
	if got != wantHex {
		return fmt.Errorf("digest mismatch: expected sha256:%s, got sha256:%s", wantHex, got)
	}
	return nil
}

// verifyFileDigest checks that the sha256 of the file at path equals wantHex.
// It streams the file so large layers do not need to be held in memory.
func verifyFileDigest(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != wantHex {
		return fmt.Errorf("digest mismatch for %s: expected sha256:%s, got sha256:%s", path, wantHex, got)
	}
	return nil
}

// encoded returns the hex part of a descriptor digest, or "" when the digest is
// absent or malformed.
//
// Every digest reaching this package comes out of a manifest, which is a file in
// the bucket that some client — or an attacker — wrote. go-digest's Encoded()
// panics on anything without a ":" separator, so a truncated or hand-edited
// manifest crashed the tool with a stack trace instead of being reported. That
// is precisely backwards for `doctor` and `stats`, whose job is to find corrupt
// manifests. Callers treat "" as "this manifest does not name a usable digest".
func encoded(d digest.Digest) string {
	if d.Validate() != nil {
		return ""
	}
	return d.Encoded()
}

// requireDigest returns the hex digest, or an error naming what was malformed.
// Used on the paths that fetch a blob by digest, so a corrupt manifest reports
// itself instead of asking storage for "blobs/sha256/" and reporting not-found.
func requireDigest(d digest.Digest, what string) (string, error) {
	e := encoded(d)
	if e == "" {
		return "", fmt.Errorf("manifest names a malformed %s digest %q", what, string(d))
	}
	return e, nil
}
