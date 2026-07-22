package image

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
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
