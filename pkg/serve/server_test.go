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

	"github.com/OuFinx/s3lo/v2/pkg/storage"
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
func (f *fakeBackend) TouchObject(_ context.Context, _, _ string) error {
	panic("TouchObject not expected in serve tests")
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

func TestGetChildManifestByDigestFromIndex(t *testing.T) {
	b := newFakeBackend()
	child := []byte(sampleManifest)
	childHash := sha256.Sum256(child)
	childDigest := "sha256:" + hex.EncodeToString(childHash[:])
	index := []byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + childDigest + `","size":191,"platform":{"os":"linux","architecture":"amd64"}}]}`)

	b.set("testbucket", "manifests/myapp/latest/manifest.json", index)
	b.set("testbucket", "blobs/sha256/"+hex.EncodeToString(childHash[:]), child)

	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/myapp/manifests/" + childDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET child manifest by digest = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != sampleManifest {
		t.Errorf("child digest lookup body mismatch: got %q", body)
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

// TestRejectsInvalidNameAndReference covers what used to be spliced unchecked
// into storage keys: a ".." component reached outside the image's own prefix
// (on the local backend "/v2/x/../secret/manifests/v1" answered 200), and any
// spelling of a tag was accepted.
//
// The requests go through the handler directly because an HTTP client
// normalises "x/.." out of the path before it is ever sent, which is precisely
// the case the server must not rely on.
func TestRejectsInvalidNameAndReference(t *testing.T) {
	b := newFakeBackend()
	b.set("testbucket", "manifests/secret/v1/manifest.json", []byte(sampleManifest))
	srv := &Server{Client: b, Bucket: "testbucket", PresignTTL: time.Hour}

	cases := []struct {
		path string
		code string
	}{
		{"/v2/x/../secret/manifests/v1", "NAME_INVALID"},
		{"/v2/../secret/manifests/v1", "NAME_INVALID"},
		{"/v2//manifests/v1", "NAME_INVALID"},
		{"/v2/UPPER/manifests/v1", "NAME_INVALID"},
		{"/v2/my%20app/manifests/v1", "NAME_INVALID"},      // decoded before it is checked
		{"/v2/%2e%2e/secret/manifests/v1", "NAME_INVALID"}, // percent-encoded traversal
		{"/v2/myapp/manifests/./v1", "TAG_INVALID"},
		{"/v2/myapp/manifests/x/../v1", "TAG_INVALID"},
		{"/v2/myapp/manifests/v1/", "TAG_INVALID"},
		{"/v2/myapp/manifests/.", "TAG_INVALID"},
		{"/v2/myapp/manifests/-v1", "TAG_INVALID"},
		{"/v2/myapp/manifests/sha256:notlonghexatall", "TAG_INVALID"},
		{"/v2/myapp/blobs/../secret", "DIGEST_INVALID"},
		{"/v2/myapp/blobs/sha256:zzzz", "DIGEST_INVALID"},
		{"/v2/x/../secret/blobs/sha256:" + strings.Repeat("a", 64), "NAME_INVALID"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", tc.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tc.code) {
			t.Errorf("GET %s body = %s, want %s", tc.path, rec.Body.String(), tc.code)
		}
	}
}

func TestNestedNameIsStillServed(t *testing.T) {
	b := newFakeBackend()
	b.set("testbucket", "manifests/org/app/v1.0/manifest.json", []byte(sampleManifest))
	ts := newTestServer(t, b)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/org/app/manifests/v1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET nested name = %d, want 200", resp.StatusCode)
	}
}

// TestOneTagIsOneCacheKey: every spelling of a single tag used to reach storage
// as its own cache key, so pulling one tag in a loop under "v1", "v1/", "./v1"
// and "x/../v1" grew the cache without bound when --cache-entries is 0.
func TestOneTagIsOneCacheKey(t *testing.T) {
	b := newFakeBackend()
	b.set("testbucket", "manifests/myapp/v1/manifest.json", []byte(sampleManifest))
	c := NewCache(0, time.Minute) // 0 = unlimited, the dangerous setting
	srv := &Server{Client: b, Bucket: "testbucket", PresignTTL: time.Hour, Cache: c}

	for _, ref := range []string{"v1", "v1/", "./v1", "x/../v1", ".", "..", "v1//"} {
		req := httptest.NewRequest(http.MethodGet, "/v2/myapp/manifests/"+ref, nil)
		srv.ServeHTTP(httptest.NewRecorder(), req)
	}

	if c.Len() != 1 {
		t.Errorf("cache holds %d entries for one tag, want 1", c.Len())
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

	// Well-formed digest, absent blob: a malformed digest is a 400 (see
	// TestRejectsInvalidNameAndReference), so this must be full-length to reach
	// the lookup at all.
	resp, err := http.Get(ts.URL + "/v2/myapp/blobs/sha256:" + strings.Repeat("d", 64))
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
