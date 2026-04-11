package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/OuFinx/s3lo/pkg/ref"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// CopyResult summarizes a copy operation.
type CopyResult struct {
	BlobsCopied  int
	BlobsSkipped int
	Platforms    int // number of platforms copied (1 for single-arch)
}

// CopyOptions controls copy behavior.
type CopyOptions struct {
	// Platform filters to a specific platform (e.g. "linux/amd64").
	// Empty means copy all platforms.
	Platform string
}

// Copy copies an image from src to dest.
// src can be:
//   - s3://bucket/image:tag  (S3 source)
//   - <registry>/<image>:<tag>  (OCI registry, e.g. ECR or Docker Hub)
//
// dest must be s3://bucket/image:tag.
func Copy(ctx context.Context, src, destRef string, opts CopyOptions) (*CopyResult, error) {
	if strings.HasPrefix(src, "s3://") {
		return copyS3ToS3(ctx, src, destRef, opts)
	}
	return copyRegistryToS3(ctx, src, destRef, opts)
}

// copyS3ToS3 copies an image between S3 locations.
// Uses server-side S3 CopyObject within the same bucket; streams cross-bucket.
func copyS3ToS3(ctx context.Context, srcRef, destRef string, opts CopyOptions) (*CopyResult, error) {
	srcParsed, err := ref.Parse(srcRef)
	if err != nil {
		return nil, fmt.Errorf("invalid source S3 reference: %w", err)
	}
	destParsed, err := ref.Parse(destRef)
	if err != nil {
		return nil, fmt.Errorf("invalid destination S3 reference: %w", err)
	}

	client, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	manifestKey := srcParsed.ManifestsPrefix() + "manifest.json"
	manifestData, err := client.GetObject(ctx, srcParsed.Bucket, manifestKey)
	if err != nil {
		return nil, fmt.Errorf("fetch source manifest: %w", err)
	}

	result := &CopyResult{}
	sameBucket := srcParsed.Bucket == destParsed.Bucket

	copyBlob := func(digest string) error {
		srcKey := "blobs/sha256/" + digest
		destKey := "blobs/sha256/" + digest
		exists, _ := client.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if exists {
			result.BlobsSkipped++
			return nil
		}
		if sameBucket {
			if err := client.CopyObject(ctx, srcParsed.Bucket, srcKey, destKey); err != nil {
				return fmt.Errorf("copy blob %s: %w", digest[:12], err)
			}
		} else {
			data, err := client.GetObject(ctx, srcParsed.Bucket, srcKey)
			if err != nil {
				return fmt.Errorf("download blob %s: %w", digest[:12], err)
			}
			tmp, err := writeTempFile(data)
			if err != nil {
				return err
			}
			defer os.Remove(tmp)
			if err := client.UploadFile(ctx, tmp, destParsed.Bucket, destKey, s3types.StorageClassIntelligentTiering); err != nil {
				return fmt.Errorf("upload blob %s: %w", digest[:12], err)
			}
		}
		result.BlobsCopied++
		return nil
	}

	if isImageIndex(manifestData) {
		idx, err := parseIndex(manifestData)
		if err != nil {
			return nil, fmt.Errorf("parse image index: %w", err)
		}

		// Filter platforms if requested.
		var selected []ocispec.Descriptor
		for _, desc := range idx.Manifests {
			if opts.Platform != "" && !matchesPlatform(desc, opts.Platform) {
				continue
			}
			selected = append(selected, desc)
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("no matching platform found (available: %s)", indexPlatformList(idx))
		}

		for _, desc := range selected {
			d := desc.Digest.Encoded()
			// Copy platform manifest blob.
			if err := copyBlob(d); err != nil {
				return nil, err
			}
			// Fetch and copy the platform manifest's blobs.
			platManifestData, err := client.GetObject(ctx, srcParsed.Bucket, "blobs/sha256/"+d)
			if err != nil {
				return nil, fmt.Errorf("fetch platform manifest %s: %w", d[:12], err)
			}
			var platManifest struct {
				Config struct{ Digest string `json:"digest"` } `json:"config"`
				Layers []struct{ Digest string `json:"digest"` } `json:"layers"`
			}
			if err := json.Unmarshal(platManifestData, &platManifest); err != nil {
				return nil, fmt.Errorf("parse platform manifest: %w", err)
			}
			if err := copyBlob(trimSHA256Prefix(platManifest.Config.Digest)); err != nil {
				return nil, err
			}
			for _, layer := range platManifest.Layers {
				if err := copyBlob(trimSHA256Prefix(layer.Digest)); err != nil {
					return nil, err
				}
			}
			result.Platforms++
		}

		// Write manifest files.
		destPrefix := destParsed.ManifestsPrefix()
		var writeManifestData []byte
		if opts.Platform != "" && len(selected) == 1 {
			// Single platform selected: write the platform manifest directly.
			d := selected[0].Digest.Encoded()
			writeManifestData, err = client.GetObject(ctx, srcParsed.Bucket, "blobs/sha256/"+d)
			if err != nil {
				return nil, fmt.Errorf("fetch platform manifest for write: %w", err)
			}
		} else {
			// Build a filtered index if needed, otherwise write the original.
			if opts.Platform != "" {
				filteredIdx := idx
				filteredIdx.Manifests = selected
				writeManifestData, err = json.Marshal(filteredIdx)
				if err != nil {
					return nil, fmt.Errorf("marshal filtered index: %w", err)
				}
			} else {
				writeManifestData = manifestData
			}
		}
		if err := client.PutObject(ctx, destParsed.Bucket, destPrefix+"manifest.json", writeManifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
		ociLayout := []byte(`{"imageLayoutVersion":"1.0.0"}`)
		if err := client.PutObject(ctx, destParsed.Bucket, destPrefix+"oci-layout", ociLayout); err != nil {
			return nil, fmt.Errorf("write oci-layout: %w", err)
		}
	} else {
		// Single-arch: copy blobs and manifest files.
		result.Platforms = 1
		var manifest struct {
			Config struct{ Digest string `json:"digest"` } `json:"config"`
			Layers []struct{ Digest string `json:"digest"` } `json:"layers"`
		}
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return nil, fmt.Errorf("parse source manifest: %w", err)
		}
		if err := copyBlob(trimSHA256Prefix(manifest.Config.Digest)); err != nil {
			return nil, err
		}
		for _, layer := range manifest.Layers {
			if err := copyBlob(trimSHA256Prefix(layer.Digest)); err != nil {
				return nil, err
			}
		}
		srcManifestPrefix := srcParsed.ManifestsPrefix()
		destManifestPrefix := destParsed.ManifestsPrefix()
		manifestKeys, err := client.ListKeys(ctx, srcParsed.Bucket, srcManifestPrefix)
		if err != nil {
			return nil, fmt.Errorf("list source manifest files: %w", err)
		}
		for _, key := range manifestKeys {
			rel := strings.TrimPrefix(key, srcManifestPrefix)
			destKey := destManifestPrefix + rel
			if sameBucket {
				if err := client.CopyObject(ctx, srcParsed.Bucket, key, destKey); err != nil {
					return nil, fmt.Errorf("copy manifest file %s: %w", rel, err)
				}
			} else {
				data, err := client.GetObject(ctx, srcParsed.Bucket, key)
				if err != nil {
					return nil, fmt.Errorf("download manifest file %s: %w", rel, err)
				}
				if err := client.PutObject(ctx, destParsed.Bucket, destKey, data); err != nil {
					return nil, fmt.Errorf("upload manifest file %s: %w", rel, err)
				}
			}
		}
	}

	return result, nil
}

// copyRegistryToS3 pulls an image from an OCI registry and pushes it to S3.
// Supports ECR (auto-auth via AWS SDK) and any OCI Distribution compatible registry.
// If the source is a multi-arch Image Index, all platforms are copied by default.
func copyRegistryToS3(ctx context.Context, srcRef, destRef string, opts CopyOptions) (*CopyResult, error) {
	destParsed, err := ref.Parse(destRef)
	if err != nil {
		return nil, fmt.Errorf("invalid destination S3 reference: %w", err)
	}

	reg, image, tag, err := parseOCIRef(srcRef)
	if err != nil {
		return nil, fmt.Errorf("parse source reference: %w", err)
	}

	httpClient := &http.Client{}
	token, err := resolveAuth(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("resolve registry auth: %w", err)
	}

	// Fetch manifest (accepts both single-arch and multi-arch).
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, image, tag)
	manifestData, _, err := fetchWithAuth(httpClient, manifestURL, token, reg, image)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest from registry: %w", err)
	}

	s3c, err := s3client.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	result := &CopyResult{}

	uploadBlob := func(digest string) error {
		encoded := trimSHA256Prefix(digest)
		destKey := "blobs/sha256/" + encoded
		exists, _ := s3c.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if exists {
			result.BlobsSkipped++
			return nil
		}
		blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", reg, image, digest)
		blobData, _, err := fetchWithAuth(httpClient, blobURL, token, reg, image)
		if err != nil {
			return fmt.Errorf("fetch blob %s: %w", encoded[:12], err)
		}
		tmp, err := writeTempFile(blobData)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		if err := s3c.UploadFile(ctx, tmp, destParsed.Bucket, destKey, s3types.StorageClassIntelligentTiering); err != nil {
			return fmt.Errorf("upload blob %s: %w", encoded[:12], err)
		}
		result.BlobsCopied++
		return nil
	}

	copyPlatformManifest := func(manifestBytes []byte, manifestDigest string) error {
		// Store platform manifest as a content-addressed blob using the bytes we already
		// have. Do NOT use uploadBlob() here — that fetches via the /blobs/ endpoint which
		// is wrong for manifests (manifests live at /manifests/<digest> on registries).
		encoded := trimSHA256Prefix(manifestDigest)
		destKey := "blobs/sha256/" + encoded
		exists, _ := s3c.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if !exists {
			tmp, err := writeTempFile(manifestBytes)
			if err != nil {
				return err
			}
			defer os.Remove(tmp)
			if err := s3c.UploadFile(ctx, tmp, destParsed.Bucket, destKey, s3types.StorageClassIntelligentTiering); err != nil {
				return fmt.Errorf("upload platform manifest blob: %w", err)
			}
			result.BlobsCopied++
		} else {
			result.BlobsSkipped++
		}
		// Upload the platform's config and layer blobs.
		var m ocispec.Manifest
		if err := json.Unmarshal(manifestBytes, &m); err != nil {
			return fmt.Errorf("parse platform manifest: %w", err)
		}
		if err := uploadBlob(m.Config.Digest.String()); err != nil {
			return err
		}
		for _, layer := range m.Layers {
			if err := uploadBlob(layer.Digest.String()); err != nil {
				return err
			}
		}
		return nil
	}

	manifestPrefix := destParsed.ManifestsPrefix()
	ociLayout := []byte(`{"imageLayoutVersion":"1.0.0"}`)

	if isImageIndex(manifestData) {
		idx, err := parseIndex(manifestData)
		if err != nil {
			return nil, fmt.Errorf("parse image index: %w", err)
		}

		// Filter platforms if requested.
		var selected []ocispec.Descriptor
		for _, desc := range idx.Manifests {
			if opts.Platform != "" && !matchesPlatform(desc, opts.Platform) {
				continue
			}
			selected = append(selected, desc)
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("no matching platform found (available: %s)", indexPlatformList(idx))
		}

		var writeManifestData []byte

		if opts.Platform != "" && len(selected) == 1 {
			// Single platform selected: fetch platform manifest and write it directly.
			desc := selected[0]
			platURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, image, desc.Digest.String())
			platData, _, err := fetchWithAuth(httpClient, platURL, token, reg, image)
			if err != nil {
				return nil, fmt.Errorf("fetch platform manifest: %w", err)
			}
			if err := copyPlatformManifest(platData, desc.Digest.String()); err != nil {
				return nil, err
			}
			writeManifestData = platData
			result.Platforms = 1
		} else {
			// Copy all selected platforms, write the index.
			for _, desc := range selected {
				platURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, image, desc.Digest.String())
				platData, _, err := fetchWithAuth(httpClient, platURL, token, reg, image)
				if err != nil {
					return nil, fmt.Errorf("fetch platform manifest %s: %w", platformString(desc.Platform), err)
				}
				if err := copyPlatformManifest(platData, desc.Digest.String()); err != nil {
					return nil, fmt.Errorf("copy platform %s: %w", platformString(desc.Platform), err)
				}
				result.Platforms++
			}
			if opts.Platform != "" {
				filteredIdx := idx
				filteredIdx.Manifests = selected
				writeManifestData, err = json.Marshal(filteredIdx)
				if err != nil {
					return nil, fmt.Errorf("marshal filtered index: %w", err)
				}
			} else {
				writeManifestData = manifestData
			}
		}

		if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"manifest.json", writeManifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
	} else {
		// Single-arch manifest.
		result.Platforms = 1
		var manifest ocispec.Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		if err := uploadBlob(manifest.Config.Digest.String()); err != nil {
			return nil, err
		}
		for _, layer := range manifest.Layers {
			if err := uploadBlob(layer.Digest.String()); err != nil {
				return nil, err
			}
		}
		if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"manifest.json", manifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
	}

	if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"oci-layout", ociLayout); err != nil {
		return nil, fmt.Errorf("write oci-layout: %w", err)
	}

	return result, nil
}

// resolveAuth obtains a Bearer token or Basic credentials for the given registry.
func resolveAuth(ctx context.Context, registry string) (string, error) {
	if strings.Contains(registry, ".dkr.ecr.") {
		return resolveECRAuth(ctx, registry)
	}
	// Other registries: no pre-auth, let fetchWithAuth handle 401 challenges.
	return "", nil
}

// resolveECRAuth fetches an ECR authorization token and returns it as a Basic auth header value.
func resolveECRAuth(ctx context.Context, registry string) (string, error) {
	// Extract region from hostname: 123456789.dkr.ecr.<region>.amazonaws.com
	parts := strings.Split(registry, ".")
	// parts: [123456789, dkr, ecr, <region>, amazonaws, com]
	if len(parts) < 6 {
		return "", fmt.Errorf("cannot parse ECR region from hostname %q", registry)
	}
	region := parts[3]

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return "", fmt.Errorf("load AWS config: %w", err)
	}

	ecrClient := ecr.NewFromConfig(cfg)
	resp, err := ecrClient.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", fmt.Errorf("get ECR authorization token: %w", err)
	}
	if len(resp.AuthorizationData) == 0 {
		return "", fmt.Errorf("ECR returned no authorization data")
	}

	// Token is base64-encoded "AWS:<password>".
	return *resp.AuthorizationData[0].AuthorizationToken, nil
}

// fetchWithAuth fetches a URL using Bearer or Basic auth.
// On 401 with a WWW-Authenticate challenge, it performs the token flow and retries.
func fetchWithAuth(client *http.Client, rawURL, authToken, registry, image string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", acceptHeader())

	if authToken != "" {
		// ECR uses Basic auth with the base64 token directly.
		if strings.Contains(registry, ".dkr.ecr.") {
			req.Header.Set("Authorization", "Basic "+authToken)
		} else {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	// Handle 401: perform token challenge flow.
	if resp.StatusCode == http.StatusUnauthorized && authToken == "" {
		token, err := handleChallenge(client, resp.Header.Get("WWW-Authenticate"), image)
		if err != nil {
			return nil, "", fmt.Errorf("auth challenge: %w", err)
		}
		req2, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("create retry request: %w", err)
		}
		req2.Header.Set("Accept", req.Header.Get("Accept"))
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2, err := client.Do(req2)
		if err != nil {
			return nil, "", err
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("registry returned %d for %s", resp2.StatusCode, rawURL)
		}
		data, err := io.ReadAll(resp2.Body)
		return data, resp2.Header.Get("Content-Type"), err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("registry returned %d for %s", resp.StatusCode, rawURL)
	}
	data, err := io.ReadAll(resp.Body)
	return data, resp.Header.Get("Content-Type"), err
}

// handleChallenge parses a WWW-Authenticate Bearer challenge and fetches a token.
func handleChallenge(client *http.Client, header, image string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("no WWW-Authenticate header")
	}
	// Parse: Bearer realm="...",service="...",scope="..."
	header = strings.TrimPrefix(header, "Bearer ")
	params := make(map[string]string)
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			params[kv[0]] = strings.Trim(kv[1], `"`)
		}
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("no realm in WWW-Authenticate")
	}

	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	q := tokenURL.Query()
	if svc := params["service"]; svc != "" {
		q.Set("service", svc)
	}
	if scope := params["scope"]; scope != "" {
		q.Set("scope", scope)
	}
	tokenURL.RawQuery = q.Encode()

	resp, err := client.Get(tokenURL.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	if tokenResp.Token != "" {
		return tokenResp.Token, nil
	}
	return tokenResp.AccessToken, nil
}

// parseOCIRef parses an OCI reference like "registry/image:tag" or "docker.io/library/nginx:latest".
func parseOCIRef(raw string) (registry, image, tag string, err error) {
	// Strip protocol if present.
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")

	// Find the registry (first component before first slash if it contains a dot or colon).
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid OCI reference %q: expected registry/image:tag", raw)
	}

	firstPart := parts[0]
	rest := parts[1]

	// A component is a registry if it contains a dot, colon, or is "localhost".
	if strings.ContainsAny(firstPart, ".:") || firstPart == "localhost" {
		registry = firstPart
	} else {
		// Default to Docker Hub.
		registry = "registry-1.docker.io"
		rest = raw // whole thing is image:tag
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest // e.g. "nginx:latest" -> "library/nginx:latest"
		}
	}

	// Split image and tag.
	if colonIdx := strings.LastIndex(rest, ":"); colonIdx >= 0 {
		image = rest[:colonIdx]
		tag = rest[colonIdx+1:]
	} else {
		image = rest
		tag = "latest"
	}
	return registry, image, tag, nil
}

// writeTempFile writes data to a temp file and returns its path.
func writeTempFile(data []byte) (string, error) {
	f, err := os.CreateTemp("", "s3lo-copy-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write temp file: %w", err)
	}
	return f.Name(), nil
}

// indexPlatformList returns a human-readable list of platforms in an index.
func indexPlatformList(idx ocispec.Index) string {
	var parts []string
	for _, d := range idx.Manifests {
		parts = append(parts, platformString(d.Platform))
	}
	return strings.Join(parts, ", ")
}
