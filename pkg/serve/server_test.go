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

// presigningBackend wraps fakeBackend and implements Presigner.
type presigningBackend struct {
	*fakeBackend
	presignURL string
}

func (p *presigningBackend) PresignGetObject(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	return p.presignURL, nil
}

func TestGetBlobPresignedRedirect(t *testing.T) {
	blobData := []byte("fake blob content")
	// Serve a fake blob endpoint to receive the redirect
	blobSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(blobData)
	}))
	defer blobSrv.Close()

	fb := newFakeBackend()
	h := sha256.Sum256(blobData)
	hexStr := hex.EncodeToString(h[:])
	fb.set("testbucket", "blobs/sha256/"+hexStr, blobData)

	b := &presigningBackend{fakeBackend: fb, presignURL: blobSrv.URL + "/presigned-blob"}
	srv := &Server{Client: b, Bucket: "testbucket", PresignTTL: time.Hour}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Don't follow redirect — assert the 303 itself
	noRedirectClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirectClient.Get(ts.URL + "/v2/myapp/blobs/sha256:" + hexStr)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("GET blob (presigned) = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "presigned-blob") {
		t.Errorf("Location = %q, want presigned URL", loc)
	}
}

func TestGetBlobStream(t *testing.T) {
	b := newFakeBackend()
	blobData := []byte("streamed blob content")
	h := sha256.Sum256(blobData)
	hexStr := hex.EncodeToString(h[:])
	b.set("testbucket", "blobs/sha256/"+hexStr, blobData)
	ts := newTestServer(t, b) // fakeBackend does NOT implement Presigner → streaming
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/blobs/sha256:" + hexStr)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET blob (stream) = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != string(blobData) {
		t.Errorf("blob body mismatch: got %q, want %q", body, blobData)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl == "" {
		t.Error("Content-Length header missing")
	}
}

func TestGetBlobMissing(t *testing.T) {
	b := newFakeBackend()
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/blobs/sha256:deadbeef1234")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET missing blob = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "BLOB_UNKNOWN") {
		t.Errorf("body missing BLOB_UNKNOWN, got: %s", body)
	}
}

func TestHeadBlob(t *testing.T) {
	b := newFakeBackend()
	blobData := []byte("some blob")
	h := sha256.Sum256(blobData)
	hexStr := hex.EncodeToString(h[:])
	b.set("testbucket", "blobs/sha256/"+hexStr, blobData)
	ts := newTestServer(t, b)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodHead, ts.URL+"/v2/myapp/blobs/sha256:"+hexStr, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD blob = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("HEAD blob must have no body, got %d bytes", len(body))
	}
	if dgst := resp.Header.Get("Docker-Content-Digest"); dgst == "" {
		t.Error("HEAD blob: Docker-Content-Digest header missing")
	}
}
