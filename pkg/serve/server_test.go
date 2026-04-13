package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OuFinx/s3lo/pkg/storage"
)

// fakeBackend is a test double for storage.Backend.
type fakeBackend struct {
	objects map[string][]byte // "bucket/key" → data
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{objects: make(map[string][]byte)}
}

func (f *fakeBackend) set(bucket, key string, data []byte) {
	f.objects[bucket+"/"+key] = data
}

func (f *fakeBackend) GetObject(_ context.Context, bucket, key string) ([]byte, error) {
	data, ok := f.objects[bucket+"/"+key]
	if !ok {
		return nil, &fakeNotFoundError{key: key}
	}
	return data, nil
}

func (f *fakeBackend) HeadObjectExists(_ context.Context, bucket, key string) (bool, error) {
	_, ok := f.objects[bucket+"/"+key]
	return ok, nil
}

func (f *fakeBackend) ListKeys(_ context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	for k := range f.objects {
		bktKey := bucket + "/"
		if strings.HasPrefix(k, bktKey) {
			rel := strings.TrimPrefix(k, bktKey)
			if strings.HasPrefix(rel, prefix) {
				keys = append(keys, rel)
			}
		}
	}
	return keys, nil
}

// Unused Backend methods — panic if called unexpectedly in tests.
func (f *fakeBackend) PutObject(_ context.Context, _, _ string, _ []byte) error {
	panic("PutObject not expected in serve tests")
}
func (f *fakeBackend) ListObjectsWithMeta(_ context.Context, _, _ string) ([]storage.ObjectMeta, error) {
	panic("ListObjectsWithMeta not expected in serve tests")
}
func (f *fakeBackend) DeleteObjects(_ context.Context, _ string, _ []string) error {
	panic("DeleteObjects not expected in serve tests")
}
func (f *fakeBackend) UploadFile(_ context.Context, _, _, _ string, _ storage.StorageClass) error {
	panic("UploadFile not expected in serve tests")
}
func (f *fakeBackend) DownloadObjectToFile(_ context.Context, _, _, _ string) error {
	panic("DownloadObjectToFile not expected in serve tests")
}
func (f *fakeBackend) DownloadDirectory(_ context.Context, _, _, _ string) error {
	panic("DownloadDirectory not expected in serve tests")
}
func (f *fakeBackend) CopyObject(_ context.Context, _, _, _ string) error {
	panic("CopyObject not expected in serve tests")
}

type fakeNotFoundError struct{ key string }

func (e *fakeNotFoundError) Error() string { return "object not found: " + e.key }

func newTestServer(t *testing.T, b storage.Backend) *httptest.Server {
	t.Helper()
	srv := &Server{Client: b, Bucket: "testbucket", PresignTTL: time.Hour}
	return httptest.NewServer(srv)
}

func TestVersionCheck(t *testing.T) {
	b := newFakeBackend()
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /v2/ = %d, want 200", resp.StatusCode)
	}
}

func TestUnknownPath(t *testing.T) {
	b := newFakeBackend()
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /unknown = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "UNSUPPORTED") {
		t.Errorf("body missing UNSUPPORTED, got: %s", body)
	}
}

const sampleManifest = `{"mediaType":"application/vnd.oci.image.manifest.v1+json","schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:abc","size":100},"layers":[]}`

func TestGetManifestByTag(t *testing.T) {
	b := newFakeBackend()
	b.set("testbucket", "manifests/myapp/latest/manifest.json", []byte(sampleManifest))
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/manifests/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET manifest by tag = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Docker-Content-Digest") == "" {
		t.Error("Docker-Content-Digest header missing")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.oci.image.manifest.v1+json" {
		t.Errorf("Content-Type = %q, want OCI manifest type", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != sampleManifest {
		t.Errorf("body mismatch: got %q", body)
	}
}

func TestHeadManifest(t *testing.T) {
	b := newFakeBackend()
	b.set("testbucket", "manifests/myapp/latest/manifest.json", []byte(sampleManifest))
	ts := newTestServer(t, b)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodHead, ts.URL+"/v2/myapp/manifests/latest", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD manifest = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("HEAD response must have no body, got %d bytes", len(body))
	}
	if resp.Header.Get("Docker-Content-Digest") == "" {
		t.Error("HEAD: Docker-Content-Digest missing")
	}
}

func TestGetManifestByDigest(t *testing.T) {
	b := newFakeBackend()
	data := []byte(sampleManifest)
	h := sha256.Sum256(data)
	dgst := "sha256:" + hex.EncodeToString(h[:])
	b.set("testbucket", "manifests/myapp/v1.0/manifest.json", data)
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/manifests/" + dgst)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET manifest by digest = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != sampleManifest {
		t.Errorf("digest lookup body mismatch: got %q", body)
	}
}

func TestGetManifestMissing(t *testing.T) {
	b := newFakeBackend()
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/manifests/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET missing manifest = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "MANIFEST_UNKNOWN") {
		t.Errorf("body missing MANIFEST_UNKNOWN, got: %s", body)
	}
}

func TestGetManifestByDigestMissing(t *testing.T) {
	b := newFakeBackend()
	// Store one manifest, look up by a non-matching digest
	b.set("testbucket", "manifests/myapp/v1.0/manifest.json", []byte(sampleManifest))
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/manifests/sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET manifest by missing digest = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "MANIFEST_UNKNOWN") {
		t.Errorf("body missing MANIFEST_UNKNOWN, got: %s", body)
	}
}
