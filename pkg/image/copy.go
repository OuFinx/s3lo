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
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
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
	// OnBlob is called for each content blob (config or layer) after it is processed.
	// platform is the OCI platform string for multi-arch images (e.g. "linux/amd64"),
	// empty for single-arch. size is bytes; skipped is true if the blob already existed.
	OnBlob func(platform, digest string, size int64, skipped bool)
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
// All blobs are copied in parallel (up to 10 concurrent workers).
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

	sameBucket := srcParsed.Bucket == destParsed.Bucket
	var blobsCopied, blobsSkipped atomic.Int64

	// copyBlob copies one content blob. platform="" suppresses OnBlob notification
	// (used for internal platform manifest blobs the user doesn't need to see).
	copyBlob := func(ctx context.Context, digest string, size int64, platform string) error {
		srcKey := "blobs/sha256/" + digest
		destKey := "blobs/sha256/" + digest
		exists, _ := client.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if exists {
			blobsSkipped.Add(1)
			if platform != "" && opts.OnBlob != nil {
				opts.OnBlob(platform, digest, size, true)
			}
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
		blobsCopied.Add(1)
		if platform != "" && opts.OnBlob != nil {
			opts.OnBlob(platform, digest, size, false)
		}
		return nil
	}

	// blobTask describes one blob to copy.
	type blobTask struct {
		digest   string
		size     int64
		platform string // empty = internal blob (platform manifest JSON), not reported
	}

	// runParallel copies a deduplicated set of blob tasks with up to 10 workers.
	runParallel := func(tasks []blobTask) error {
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(10)
		for _, t := range tasks {
			t := t
			g.Go(func() error {
				return copyBlob(gCtx, t.digest, t.size, t.platform)
			})
		}
		return g.Wait()
	}

	destPrefix := destParsed.ManifestsPrefix()
	ociLayout := []byte(`{"imageLayoutVersion":"1.0.0"}`)

	if isImageIndex(manifestData) {
		idx, err := parseIndex(manifestData)
		if err != nil {
			return nil, fmt.Errorf("parse image index: %w", err)
		}

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

		// Fetch all platform manifests in parallel.
		type platInfo struct {
			platform string
			data     []byte
			digest   string
		}
		platInfos := make([]platInfo, len(selected))
		{
			g, gCtx := errgroup.WithContext(ctx)
			for i, desc := range selected {
				i, desc := i, desc
				g.Go(func() error {
					d := desc.Digest.Encoded()
					data, err := client.GetObject(gCtx, srcParsed.Bucket, "blobs/sha256/"+d)
					if err != nil {
						return fmt.Errorf("fetch manifest %s: %w", platformString(desc.Platform), err)
					}
					platInfos[i] = platInfo{platformString(desc.Platform), data, d}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Collect all unique blobs across all platforms (deduplicated).
		seen := make(map[string]struct{})
		var tasks []blobTask
		for _, pi := range platInfos {
			// Platform manifest blob — internal, not reported.
			if _, ok := seen[pi.digest]; !ok {
				seen[pi.digest] = struct{}{}
				tasks = append(tasks, blobTask{pi.digest, 0, ""})
			}
			var m struct {
				Config struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				} `json:"config"`
				Layers []struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(pi.data, &m); err != nil {
				return nil, fmt.Errorf("parse manifest for %s: %w", pi.platform, err)
			}
			if d := trimSHA256Prefix(m.Config.Digest); d != "" {
				if _, ok := seen[d]; !ok {
					seen[d] = struct{}{}
					tasks = append(tasks, blobTask{d, m.Config.Size, pi.platform})
				}
			}
			for _, layer := range m.Layers {
				if d := trimSHA256Prefix(layer.Digest); d != "" {
					if _, ok := seen[d]; !ok {
						seen[d] = struct{}{}
						tasks = append(tasks, blobTask{d, layer.Size, pi.platform})
					}
				}
			}
		}

		if err := runParallel(tasks); err != nil {
			return nil, err
		}

		// Write destination manifest.
		var writeManifestData []byte
		if opts.Platform != "" && len(selected) == 1 {
			writeManifestData = platInfos[0].data
		} else if opts.Platform != "" {
			filteredIdx := idx
			filteredIdx.Manifests = selected
			if writeManifestData, err = json.Marshal(filteredIdx); err != nil {
				return nil, fmt.Errorf("marshal filtered index: %w", err)
			}
		} else {
			writeManifestData = manifestData
		}
		if err := client.PutObject(ctx, destParsed.Bucket, destPrefix+"manifest.json", writeManifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
		if err := client.PutObject(ctx, destParsed.Bucket, destPrefix+"oci-layout", ociLayout); err != nil {
			return nil, fmt.Errorf("write oci-layout: %w", err)
		}
		return &CopyResult{
			Platforms:    len(selected),
			BlobsCopied:  int(blobsCopied.Load()),
			BlobsSkipped: int(blobsSkipped.Load()),
		}, nil
	}

	// Single-arch: collect blobs from manifest, copy in parallel.
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parse source manifest: %w", err)
	}
	var tasks []blobTask
	if d := trimSHA256Prefix(manifest.Config.Digest); d != "" {
		tasks = append(tasks, blobTask{d, manifest.Config.Size, ""})
	}
	for _, layer := range manifest.Layers {
		if d := trimSHA256Prefix(layer.Digest); d != "" {
			tasks = append(tasks, blobTask{d, layer.Size, ""})
		}
	}
	if err := runParallel(tasks); err != nil {
		return nil, err
	}

	// Copy manifest files.
	srcManifestPrefix := srcParsed.ManifestsPrefix()
	manifestKeys, err := client.ListKeys(ctx, srcParsed.Bucket, srcManifestPrefix)
	if err != nil {
		return nil, fmt.Errorf("list source manifest files: %w", err)
	}
	for _, key := range manifestKeys {
		rel := strings.TrimPrefix(key, srcManifestPrefix)
		destKey := destPrefix + rel
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
	return &CopyResult{
		Platforms:    1,
		BlobsCopied:  int(blobsCopied.Load()),
		BlobsSkipped: int(blobsSkipped.Load()),
	}, nil
}

// copyRegistryToS3 pulls an image from an OCI registry and pushes it to S3.
// Supports ECR (auto-auth via AWS SDK) and any OCI Distribution compatible registry.
// If the source is a multi-arch Image Index, all platforms are copied by default.
// All blobs are fetched and uploaded in parallel (up to 10 concurrent workers).
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

	var blobsCopied, blobsSkipped atomic.Int64

	// uploadBlobBytes stores already-fetched bytes as a content-addressed S3 blob.
	// Used for platform manifest blobs (which cannot be fetched via /blobs/ endpoint).
	// platform="" suppresses OnBlob notification.
	uploadBlobBytes := func(ctx context.Context, digest string, data []byte, platform string) error {
		encoded := trimSHA256Prefix(digest)
		destKey := "blobs/sha256/" + encoded
		exists, _ := s3c.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if exists {
			blobsSkipped.Add(1)
			return nil
		}
		tmp, err := writeTempFile(data)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		if err := s3c.UploadFile(ctx, tmp, destParsed.Bucket, destKey, s3types.StorageClassIntelligentTiering); err != nil {
			return fmt.Errorf("upload blob %s: %w", encoded[:12], err)
		}
		blobsCopied.Add(1)
		return nil
	}

	// fetchAndUploadBlob fetches a blob from the registry and uploads it to S3.
	// platform="" suppresses OnBlob notification (used for internal blobs).
	fetchAndUploadBlob := func(ctx context.Context, digest string, knownSize int64, platform string) error {
		encoded := trimSHA256Prefix(digest)
		destKey := "blobs/sha256/" + encoded
		exists, _ := s3c.HeadObjectExists(ctx, destParsed.Bucket, destKey)
		if exists {
			blobsSkipped.Add(1)
			if platform != "" && opts.OnBlob != nil {
				opts.OnBlob(platform, encoded, knownSize, true)
			}
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
		blobsCopied.Add(1)
		if platform != "" && opts.OnBlob != nil {
			opts.OnBlob(platform, encoded, int64(len(blobData)), false)
		}
		return nil
	}

	// blobTask describes one content blob to fetch from the registry and upload.
	type blobTask struct {
		digest   string
		size     int64
		platform string // empty = internal blob (platform manifest), not reported
	}

	// runParallel fetches and uploads a deduplicated set of blob tasks with up to 10 workers.
	runParallel := func(tasks []blobTask) error {
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(10)
		for _, t := range tasks {
			t := t
			g.Go(func() error {
				return fetchAndUploadBlob(gCtx, t.digest, t.size, t.platform)
			})
		}
		return g.Wait()
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

		// Fetch all platform manifests in parallel.
		type platInfo struct {
			platform string
			data     []byte
			digest   string
		}
		platInfos := make([]platInfo, len(selected))
		{
			g, _ := errgroup.WithContext(ctx)
			for i, desc := range selected {
				i, desc := i, desc
				g.Go(func() error {
					platURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", reg, image, desc.Digest.String())
					data, _, err := fetchWithAuth(httpClient, platURL, token, reg, image)
					if err != nil {
						return fmt.Errorf("fetch platform manifest %s: %w", platformString(desc.Platform), err)
					}
					platInfos[i] = platInfo{platformString(desc.Platform), data, desc.Digest.String()}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Store all platform manifest blobs in parallel (bytes already in memory).
		{
			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(10)
			for _, pi := range platInfos {
				pi := pi
				g.Go(func() error {
					return uploadBlobBytes(gCtx, pi.digest, pi.data, "")
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		}

		// Collect all unique content blobs (configs + layers) across platforms.
		seen := make(map[string]struct{})
		var tasks []blobTask
		for _, pi := range platInfos {
			var m struct {
				Config struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				} `json:"config"`
				Layers []struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				} `json:"layers"`
			}
			if err := json.Unmarshal(pi.data, &m); err != nil {
				return nil, fmt.Errorf("parse manifest for %s: %w", pi.platform, err)
			}
			if d := m.Config.Digest; d != "" {
				if _, ok := seen[d]; !ok {
					seen[d] = struct{}{}
					tasks = append(tasks, blobTask{d, m.Config.Size, pi.platform})
				}
			}
			for _, layer := range m.Layers {
				if d := layer.Digest; d != "" {
					if _, ok := seen[d]; !ok {
						seen[d] = struct{}{}
						tasks = append(tasks, blobTask{d, layer.Size, pi.platform})
					}
				}
			}
		}

		if err := runParallel(tasks); err != nil {
			return nil, err
		}

		// Write destination manifest.
		var writeManifestData []byte
		if opts.Platform != "" && len(selected) == 1 {
			writeManifestData = platInfos[0].data
		} else if opts.Platform != "" {
			filteredIdx := idx
			filteredIdx.Manifests = selected
			if writeManifestData, err = json.Marshal(filteredIdx); err != nil {
				return nil, fmt.Errorf("marshal filtered index: %w", err)
			}
		} else {
			writeManifestData = manifestData
		}

		if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"manifest.json", writeManifestData); err != nil {
			return nil, fmt.Errorf("write manifest.json: %w", err)
		}
		if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"oci-layout", ociLayout); err != nil {
			return nil, fmt.Errorf("write oci-layout: %w", err)
		}
		return &CopyResult{
			Platforms:    len(selected),
			BlobsCopied:  int(blobsCopied.Load()),
			BlobsSkipped: int(blobsSkipped.Load()),
		}, nil
	}

	// Single-arch manifest: collect blobs, upload in parallel.
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	var tasks []blobTask
	if d := manifest.Config.Digest; d != "" {
		tasks = append(tasks, blobTask{d, manifest.Config.Size, ""})
	}
	for _, layer := range manifest.Layers {
		if d := layer.Digest; d != "" {
			tasks = append(tasks, blobTask{d, layer.Size, ""})
		}
	}
	if err := runParallel(tasks); err != nil {
		return nil, err
	}
	if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"manifest.json", manifestData); err != nil {
		return nil, fmt.Errorf("write manifest.json: %w", err)
	}
	if err := s3c.PutObject(ctx, destParsed.Bucket, manifestPrefix+"oci-layout", ociLayout); err != nil {
		return nil, fmt.Errorf("write oci-layout: %w", err)
	}
	return &CopyResult{
		Platforms:    1,
		BlobsCopied:  int(blobsCopied.Load()),
		BlobsSkipped: int(blobsSkipped.Load()),
	}, nil
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

// parseOCIRef parses an OCI reference into registry, image, and tag.
// Supports all standard forms:
//
//	alpine                               → docker.io / library/alpine / latest
//	alpine:3.18                          → docker.io / library/alpine / 3.18
//	nginx:latest                         → docker.io / library/nginx  / latest
//	user/myapp:v1.0                      → docker.io / user/myapp     / v1.0
//	docker.io/library/alpine:latest      → docker.io / library/alpine / latest
//	ghcr.io/owner/image:tag              → ghcr.io   / owner/image    / tag
//	123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1  → ECR registry
func parseOCIRef(raw string) (registry, image, tag string, err error) {
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")

	parts := strings.SplitN(raw, "/", 2)

	// Determine whether the first component is a registry hostname.
	// A hostname contains a dot or colon, or is "localhost".
	isRegistry := len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost")

	var rest string
	if isRegistry {
		registry = parts[0]
		rest = parts[1]
	} else {
		// No registry prefix — default to Docker Hub.
		registry = "registry-1.docker.io"
		rest = raw
		// Official images (no slash) get the "library/" namespace.
		// e.g. "alpine:3.18" → "library/alpine:3.18"
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
	}

	// Split image name and tag on the last colon.
	if colonIdx := strings.LastIndex(rest, ":"); colonIdx >= 0 {
		image = rest[:colonIdx]
		tag = rest[colonIdx+1:]
	} else {
		image = rest
		tag = "latest"
	}

	if image == "" {
		return "", "", "", fmt.Errorf("invalid OCI reference %q: could not parse image name", raw)
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
