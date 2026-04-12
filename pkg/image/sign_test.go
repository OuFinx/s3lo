package image

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
)

func TestKeyIDSlug(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	slug1, err := keyIDSlug(priv.Public())
	if err != nil {
		t.Fatalf("keyIDSlug: %v", err)
	}
	if len(slug1) != 16 {
		t.Errorf("slug length = %d, want 16", len(slug1))
	}
	// Same key → same slug.
	slug2, _ := keyIDSlug(priv.Public())
	if slug1 != slug2 {
		t.Errorf("slug not deterministic: %q vs %q", slug1, slug2)
	}
	// Different key → different slug.
	priv2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	slug3, _ := keyIDSlug(priv2.Public())
	if slug1 == slug3 {
		t.Error("different keys produced same slug")
	}
}

func TestSigningPayload(t *testing.T) {
	digest := "sha256:abc123"
	payload := signingPayload(digest)
	want := []byte("sha256:abc123\n")
	if string(payload) != string(want) {
		t.Errorf("payload = %q, want %q", payload, want)
	}
}
