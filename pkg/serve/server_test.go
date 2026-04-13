package serve

import (
	"context"
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

// Unused Backend methods — panic if called unexpectedly.
func (f *fakeBackend) PutObject(_ context.Context, _, _ string, _ []byte) error { return nil }
func (f *fakeBackend) ListObjectsWithMeta(_ context.Context, _, _ string) ([]storage.ObjectMeta, error) {
	return nil, nil
}
func (f *fakeBackend) DeleteObjects(_ context.Context, _ string, _ []string) error { return nil }
func (f *fakeBackend) UploadFile(_ context.Context, _, _, _ string, _ storage.StorageClass) error {
	return nil
}
func (f *fakeBackend) DownloadObjectToFile(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeBackend) DownloadDirectory(_ context.Context, _, _, _ string) error    { return nil }
func (f *fakeBackend) CopyObject(_ context.Context, _, _, _ string) error           { return nil }

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
