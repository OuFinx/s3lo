package image

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/cosign/v2/pkg/cosign"
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

func TestSign_Roundtrip(t *testing.T) {
	// Generate ephemeral key pair.
	pass := func(_ bool) ([]byte, error) { return []byte("testpass"), nil }
	keys, err := cosign.GenerateKeyPair(pass)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "cosign.key")
	if err := os.WriteFile(keyFile, keys.PrivateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSIGN_PASSWORD", "testpass")

	// Create a local store with a minimal manifest.
	// Use a relative path pattern (./mystore) that the ref parser accepts.
	storeDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(storeDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldCwd)

	bucketDir := filepath.Join(".", "mystore")
	manifestDir := filepath.Join(bucketDir, "manifests", "myapp", "v1.0")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	s3Ref := "local://./mystore/myapp:v1.0"
	result, err := Sign(context.Background(), s3Ref, keyFile)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if result.Digest == "" {
		t.Error("expected non-empty digest")
	}
	if result.KeyID == "" || len(result.KeyID) != 16 {
		t.Errorf("unexpected keyID %q", result.KeyID)
	}

	// Verify signature file exists.
	sigPath := filepath.Join(bucketDir, "manifests", "myapp", "v1.0", "signatures", result.KeyID+".json")
	if _, err := os.Stat(sigPath); err != nil {
		t.Errorf("signature file not found: %v", err)
	}
}
