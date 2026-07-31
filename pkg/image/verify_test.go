package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/cosign/v2/pkg/cosign"
)

// setupSignedStore creates a local store with a signed image. Returns storeDir (absolute),
// the relative bucket dir name, the key file path, and the public key file path.
func setupSignedStore(t *testing.T) (storeDir, bucketRelDir, keyFile, pubFile string) {
	t.Helper()

	pass := func(_ bool) ([]byte, error) { return []byte("testpass"), nil }
	keys, err := cosign.GenerateKeyPair(pass)
	if err != nil {
		t.Fatal(err)
	}

	keyDir := t.TempDir()
	keyFile = filepath.Join(keyDir, "cosign.key")
	pubFile = filepath.Join(keyDir, "cosign.pub")
	os.WriteFile(keyFile, keys.PrivateBytes, 0o600)
	os.WriteFile(pubFile, keys.PublicBytes, 0o644)
	t.Setenv("COSIGN_PASSWORD", "testpass")

	storeDir = t.TempDir()
	oldCwd, _ := os.Getwd()
	os.Chdir(storeDir)
	t.Cleanup(func() { os.Chdir(oldCwd) })

	bucketRelDir = "./mystore"
	manifestDir := filepath.Join(bucketRelDir, "manifests", "myapp", "v1.0")
	os.MkdirAll(manifestDir, 0o755)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	os.WriteFile(filepath.Join(manifestDir, "manifest.json"), manifest, 0o644)

	return storeDir, bucketRelDir, keyFile, pubFile
}

func TestVerify_Valid(t *testing.T) {
	_, _, keyFile, pubFile := setupSignedStore(t)

	s3Ref := "local://./mystore/myapp:v1.0"

	// Sign first.
	if _, err := Sign(context.Background(), s3Ref, keyFile); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Verify with public key file.
	result, err := Verify(context.Background(), s3Ref, pubFile)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Verified {
		t.Errorf("expected Verified=true, got Reason=%q", result.Reason)
	}
	if result.Digest == "" {
		t.Error("expected non-empty digest")
	}
	if result.SignedAt == "" {
		t.Error("expected non-empty signedAt")
	}
}

func TestVerify_MissingSignature(t *testing.T) {
	_, _, _, pubFile := setupSignedStore(t)

	s3Ref := "local://./mystore/myapp:v1.0"

	// No Sign step — verify should return Verified=false, not error.
	result, err := Verify(context.Background(), s3Ref, pubFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Verified {
		t.Error("expected Verified=false for missing signature")
	}
	if result.Reason == "" {
		t.Error("expected non-empty Reason")
	}
}

func TestVerify_TamperedManifest(t *testing.T) {
	storeDir, bucketRelDir, keyFile, pubFile := setupSignedStore(t)

	s3Ref := "local://./mystore/myapp:v1.0"

	// Sign the original manifest.
	if _, err := Sign(context.Background(), s3Ref, keyFile); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Tamper: overwrite the manifest.
	tampered := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[{"digest":"sha256:evil","size":0}]}`)
	manifestPath := filepath.Join(storeDir, bucketRelDir, "manifests", "myapp", "v1.0", "manifest.json")
	if err := os.WriteFile(manifestPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Verify(context.Background(), s3Ref, pubFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Verified {
		t.Error("expected Verified=false after tamper")
	}
}

// TestVerify_RejectsTransplantedSignature performs the attack directly: an
// adversary with bucket write access copies a legitimately signed manifest and
// its signature record onto a second tag. Both tags then hold byte-identical
// manifests and a genuine signature made by the real key.
//
// Under the old payload ("<digest>\n") this passed, because the signed bytes
// said nothing about where the manifest lived. Promotion of a staging build to
// a production tag, and rollback of a tag to an older signed release, were both
// available to anyone who could write to the bucket.
func TestVerify_RejectsTransplantedSignature(t *testing.T) {
	ctx := context.Background()

	pass := func(_ bool) ([]byte, error) { return []byte("testpass"), nil }
	keys, err := cosign.GenerateKeyPair(pass)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	keyDir := t.TempDir()
	keyFile := filepath.Join(keyDir, "cosign.key")
	pubFile := filepath.Join(keyDir, "cosign.pub")
	if err := os.WriteFile(keyFile, keys.PrivateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubFile, keys.PublicBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSIGN_PASSWORD", "testpass")

	storeDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(storeDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCwd)

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	write := func(image, tag string) string {
		dir := filepath.Join(".", "mystore", "manifests", image, tag)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	// Same bytes published at a signed staging tag and an unsigned prod tag.
	signedDir := write("staging", "v1.0")
	victimDir := write("prod", "release")

	signed := "local://./mystore/staging:v1.0"
	victim := "local://./mystore/prod:release"

	res, err := Sign(ctx, signed, keyFile)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// The attack: lift the signature record verbatim onto the victim tag.
	sigName := res.KeyID + ".json"
	sigBytes, err := os.ReadFile(filepath.Join(signedDir, "signatures", sigName))
	if err != nil {
		t.Fatalf("read signature: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(victimDir, "signatures"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victimDir, "signatures", sigName), sigBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// The genuine tag must still verify.
	if got, err := Verify(ctx, signed, pubFile); err != nil {
		t.Fatalf("verify signed: %v", err)
	} else if !got.Verified {
		t.Fatalf("legitimately signed image failed to verify: %s", got.Reason)
	}

	// The transplanted one must not.
	got, err := Verify(ctx, victim, pubFile)
	if err != nil {
		t.Fatalf("verify victim: %v", err)
	}
	if got.Verified {
		t.Fatal("SECURITY: a signature transplanted from another image verified")
	}
	t.Logf("transplant rejected: %s", got.Reason)
}
