package image

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/sigstore/cosign/v2/pkg/cosign"
	cosignsig "github.com/sigstore/cosign/v2/pkg/signature"

	"github.com/OuFinx/s3lo/v3/pkg/ref"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
)

// SignatureSchemaVersion is the current signature record schema.
//
// Version 2 binds the signed payload to the image's bucket, name and tag (see
// ref.SigningPayload) and drops the stored payload field. Version 1 signed the
// manifest digest alone and stored the signed bytes alongside the signature,
// which let anyone with bucket write access transplant a valid signature onto
// another tag. Version 1 records are rejected, not verified.
const SignatureSchemaVersion = 2

// SignatureRecord is the JSON stored at manifests/<image>/<tag>/signatures/<slug>.json.
//
// The record deliberately carries no copy of the signed payload: verifiers
// rebuild it from the reference being checked, so nothing an attacker can write
// into this file is an input to the signature check.
type SignatureRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Digest        string `json:"digest"`
	KeyRef        string `json:"keyRef"`
	KeyID         string `json:"keyID"`
	Algorithm     string `json:"algorithm"`
	Signature     string `json:"signature"`
	SignedAt      string `json:"signedAt"`
}

// SignResult is returned by Sign.
type SignResult struct {
	Digest     string
	KeyRef     string
	KeyID      string
	StoredPath string
	SignedAt   time.Time
}

// Sign signs the manifest digest of the image identified by s3Ref and stores
// the signature at manifests/<image>/<tag>/signatures/<keyid>.json.
func Sign(ctx context.Context, s3Ref, keyRef string) (*SignResult, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	// Read manifest and compute its digest.
	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	h := sha256.Sum256(manifestData)
	digest := fmt.Sprintf("sha256:%x", h)

	// Load signer.
	sv, err := cosignsig.SignerVerifierFromKeyRef(ctx, keyRef, makePassFunc(), nil)
	if err != nil {
		return nil, fmt.Errorf("load signing key %q: %w", keyRef, err)
	}

	// Sign the payload. It binds the signature to this exact bucket/image:tag.
	payload := ref.SigningPayload(parsed.Bucket, parsed.Image, parsed.Tag, digest)
	sigBytes, err := sv.SignMessage(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	// Derive key ID slug and algorithm.
	pub, err := sv.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}
	slug, err := keyIDSlug(pub)
	if err != nil {
		return nil, fmt.Errorf("derive key ID: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	rec := SignatureRecord{
		SchemaVersion: SignatureSchemaVersion,
		Digest:        digest,
		KeyRef:        keyRef,
		KeyID:         slug,
		Algorithm:     algorithmName(pub),
		Signature:     base64.StdEncoding.EncodeToString(sigBytes),
		SignedAt:      now.Format(time.RFC3339),
	}
	recData, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("marshal signature: %w", err)
	}

	sigKey := parsed.ManifestsPrefix() + "signatures/" + slug + ".json"
	if err := client.PutObject(ctx, parsed.Bucket, sigKey, recData); err != nil {
		return nil, fmt.Errorf("store signature: %w", err)
	}

	return &SignResult{
		Digest:     digest,
		KeyRef:     keyRef,
		KeyID:      slug,
		StoredPath: parsed.Bucket + "/" + sigKey,
		SignedAt:   now,
	}, nil
}

// keyIDSlug returns a stable, filename-safe 16-char hex identifier for a public key.
// Derived from SHA-256 of the DER-encoded public key.
func keyIDSlug(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16], nil
}

// algorithmName returns a human-readable algorithm label for the public key type.
func algorithmName(pub crypto.PublicKey) string {
	switch pub.(type) {
	case *ecdsa.PublicKey:
		return "ECDSA_SHA_256"
	case *rsa.PublicKey:
		return "RSA_PKCS1_SHA_256"
	default:
		return "UNKNOWN"
	}
}

// makePassFunc returns a cosign.PassFunc that reads COSIGN_PASSWORD from the environment.
// If unset, returns an empty password (suitable for unencrypted keys or tests).
func makePassFunc() cosign.PassFunc {
	return func(_ bool) ([]byte, error) {
		if pass := os.Getenv("COSIGN_PASSWORD"); pass != "" {
			return []byte(pass), nil
		}
		return []byte{}, nil
	}
}
