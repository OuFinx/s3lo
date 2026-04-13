package image

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	cosignsig "github.com/sigstore/cosign/v2/pkg/signature"

	"github.com/OuFinx/s3lo/pkg/ref"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// VerifyResult is returned by Verify.
type VerifyResult struct {
	Verified bool   `json:"verified"`
	Reason   string `json:"reason,omitempty"` // set when Verified == false
	Digest   string `json:"digest"`
	KeyRef   string `json:"keyRef"`
	KeyID    string `json:"keyID"`
	SignedAt string `json:"signedAt,omitempty"`
}

// Verify checks whether a stored signature for s3Ref is valid against keyRef.
//
// Returns (result, nil) for all cases where infrastructure worked:
//   - result.Verified == true  → signature valid
//   - result.Verified == false → missing or invalid (caller should exit 1)
//
// Returns (nil, err) for infrastructure failures (caller should exit 2).
func Verify(ctx context.Context, s3Ref, keyRef string) (*VerifyResult, error) {
	parsed, err := ref.Parse(s3Ref)
	if err != nil {
		return nil, fmt.Errorf("invalid reference: %w", err)
	}

	client, err := storage.NewBackendFromRef(ctx, s3Ref)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}

	// Read manifest and compute digest.
	manifestKey := parsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, parsed.Bucket, manifestKey)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	h := sha256.Sum256(manifestData)
	digest := fmt.Sprintf("sha256:%x", h)

	// Load verifier. Try public-key path first; fall back to SignerVerifier for .key files.
	verifier, err := cosignsig.VerifierForKeyRef(ctx, keyRef, crypto.SHA256)
	if err != nil {
		sv, svErr := cosignsig.SignerVerifierFromKeyRef(ctx, keyRef, makePassFunc(), nil)
		if svErr != nil {
			return nil, fmt.Errorf("load verification key %q: %w", keyRef, svErr)
		}
		verifier = sv
	}

	// Derive key ID slug.
	pub, err := verifier.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}
	slug, err := keyIDSlug(pub)
	if err != nil {
		return nil, fmt.Errorf("derive key ID: %w", err)
	}

	// Load the signature record.
	sigKey := parsed.ManifestsPrefix() + "signatures/" + slug + ".json"
	sigData, err := client.GetObject(ctx, parsed.Bucket, sigKey)
	if err != nil {
		if storage.IsNotFound(err) {
			return &VerifyResult{
				Verified: false,
				Reason:   "no signature found for key " + keyRef,
				Digest:   digest,
				KeyRef:   keyRef,
				KeyID:    slug,
			}, nil
		}
		return nil, fmt.Errorf("read signature: %w", err)
	}

	var rec SignatureRecord
	if err := json.Unmarshal(sigData, &rec); err != nil {
		return nil, fmt.Errorf("parse signature record: %w", err)
	}

	// Check that the signed digest matches the current manifest.
	if rec.Digest != digest {
		return &VerifyResult{
			Verified: false,
			Reason:   fmt.Sprintf("manifest changed: signed %s, current %s", rec.Digest, digest),
			Digest:   digest,
			KeyRef:   keyRef,
			KeyID:    slug,
			SignedAt: rec.SignedAt,
		}, nil
	}

	// Verify the cryptographic signature.
	sigBytes, err := base64.StdEncoding.DecodeString(rec.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode signature bytes: %w", err)
	}
	payload := signingPayload(digest)
	if err := verifier.VerifySignature(bytes.NewReader(sigBytes), bytes.NewReader(payload)); err != nil {
		return &VerifyResult{
			Verified: false,
			Reason:   "invalid signature: " + err.Error(),
			Digest:   digest,
			KeyRef:   keyRef,
			KeyID:    slug,
			SignedAt: rec.SignedAt,
		}, nil
	}

	return &VerifyResult{
		Verified: true,
		Digest:   digest,
		KeyRef:   keyRef,
		KeyID:    slug,
		SignedAt: rec.SignedAt,
	}, nil
}
