package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
)

// retryDelays defines wait durations between successive retry attempts for transient errors.
var retryDelays = []time.Duration{time.Second, 2 * time.Second}

// registryClient wraps an HTTP client with per-registry Bearer token caching (#41)
// and automatic retry on transient errors (#45).
type registryClient struct {
	http   *http.Client
	mu     sync.Mutex
	tokens map[string]string // registry → token
}

func newRegistryClient() *registryClient {
	return &registryClient{
		http:   &http.Client{},
		tokens: make(map[string]string),
	}
}

func (rc *registryClient) getToken(registry string) string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.tokens[registry]
}

func (rc *registryClient) setToken(registry, token string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.tokens[registry] = token
}

// authHeader returns the Authorization header value for a registry and token.
// ECR uses Basic auth; all others use Bearer.
func authHeader(registry, token string) string {
	if strings.Contains(registry, ".dkr.ecr.") {
		return "Basic " + token
	}
	return "Bearer " + token
}

// doWithRetry executes fn, retrying up to len(retryDelays) additional times on network
// errors and 5xx responses. Context cancellation stops retries immediately.
func (rc *registryClient) doWithRetry(ctx context.Context, fn func() (*http.Response, error)) (*http.Response, error) {
	var lastErr error
	for i := 0; ; i++ {
		resp, err := fn()
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if err != nil {
			lastErr = err
			slog.Debug("registry request error, will retry", "attempt", i+1, "error", err)
		} else {
			lastErr = fmt.Errorf("server error: HTTP %d", resp.StatusCode)
			resp.Body.Close()
			slog.Debug("registry server error, will retry", "attempt", i+1, "status", resp.StatusCode)
		}
		if i >= len(retryDelays) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryDelays[i]):
		}
	}
	return nil, lastErr
}

// doRequest performs an authenticated GET with retry and 401-challenge handling.
// On a 401 with no cached token, it negotiates a Bearer token and caches it (#41).
func (rc *registryClient) doRequest(ctx context.Context, rawURL, registry, imageName string) (*http.Response, error) {
	slog.Debug("registry GET", "url", rawURL)

	makeReq := func() (*http.Response, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Accept", acceptHeader())
		if t := rc.getToken(registry); t != "" {
			r.Header.Set("Authorization", authHeader(registry, t))
		}
		return rc.http.Do(r)
	}

	resp, err := rc.doWithRetry(ctx, makeReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		slog.Debug("registry 401, negotiating token", "registry", registry)
		newToken, err := handleChallenge(rc.http, resp.Header.Get("WWW-Authenticate"), imageName)
		if err != nil {
			return nil, fmt.Errorf("auth challenge: %w", err)
		}
		rc.setToken(registry, newToken)

		resp, err = rc.doWithRetry(ctx, func() (*http.Response, error) {
			r, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
			if err != nil {
				return nil, err
			}
			r.Header.Set("Accept", acceptHeader())
			r.Header.Set("Authorization", "Bearer "+newToken)
			return rc.http.Do(r)
		})
		if err != nil {
			return nil, err
		}
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("registry returned %d for %s", resp.StatusCode, rawURL)
	}
	return resp, nil
}

// fetchManifest fetches a manifest URL and returns its bytes and content-type.
func (rc *registryClient) fetchManifest(ctx context.Context, rawURL, registry, imageName string) ([]byte, string, error) {
	resp, err := rc.doRequest(ctx, rawURL, registry, imageName)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.Header.Get("Content-Type"), err
}

// fetchBlobToFile streams a blob from the registry directly to a temporary file (#40).
// Returns the temp file path; the caller is responsible for removing it.
func (rc *registryClient) fetchBlobToFile(ctx context.Context, rawURL, registry, imageName string) (string, error) {
	resp, err := rc.doRequest(ctx, rawURL, registry, imageName)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp("", "s3lo-blob-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("stream blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	return tmpName, nil
}

// resolveAuth obtains initial auth credentials for the given registry.
// Returns "" for non-ECR registries — those use 401 challenge flow.
func resolveAuth(ctx context.Context, registry string) (string, error) {
	if strings.Contains(registry, ".dkr.ecr.") {
		return resolveECRAuth(ctx, registry)
	}
	return "", nil
}

// resolveECRAuth fetches an ECR authorization token and returns it as a Basic auth value.
func resolveECRAuth(ctx context.Context, registry string) (string, error) {
	// Extract region from hostname: 123456789.dkr.ecr.<region>.amazonaws.com
	parts := strings.Split(registry, ".")
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
	return *resp.AuthorizationData[0].AuthorizationToken, nil
}

// handleChallenge parses a WWW-Authenticate Bearer challenge and fetches a token.
func handleChallenge(client *http.Client, header, imageName string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("no WWW-Authenticate header")
	}
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

	slog.Debug("fetching registry token", "url", tokenURL.String())
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
