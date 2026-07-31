package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// countingObserver records what the server reports without pulling in a metrics
// library, which is the whole point of the Observer indirection.
type countingObserver struct {
	mu        sync.Mutex
	manifests map[string]int
	blobs     map[string]int
	storage   map[string]int
}

func newCountingObserver() *countingObserver {
	return &countingObserver{
		manifests: map[string]int{},
		blobs:     map[string]int{},
		storage:   map[string]int{},
	}
}

func (o *countingObserver) ManifestServed(source string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.manifests[source]++
}

func (o *countingObserver) BlobServed(status string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.blobs[status]++
}

func (o *countingObserver) StorageCall(op string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.storage[op]++
}

func (o *countingObserver) count(m map[string]int, k string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return m[k]
}

const testManifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"sha256:aa"},"layers":[]}`

// TestMultiBucketMode covers the addressing a node-local proxy needs: the bucket
// comes from the first path segment instead of from configuration, so one server
// can front every bucket an account has.
func TestMultiBucketMode(t *testing.T) {
	b := newFakeBackend()
	b.set("bucket-one", "manifests/myapp/v1/manifest.json", []byte(testManifest))
	b.set("bucket-two", "manifests/myapp/v1/manifest.json", []byte(testManifest))

	// Bucket left empty => multi-bucket.
	ts := httptest.NewServer(&Server{Client: b, PresignTTL: time.Hour})
	defer ts.Close()

	for _, bucket := range []string{"bucket-one", "bucket-two"} {
		resp, err := http.Get(ts.URL + "/v2/" + bucket + "/myapp/manifests/v1")
		if err != nil {
			t.Fatalf("GET %s: %v", bucket, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", bucket, resp.StatusCode, body)
		}
		if string(body) != testManifest {
			t.Errorf("%s: unexpected manifest body", bucket)
		}
	}

	// A path with no bucket segment must not be silently treated as an image.
	resp, err := http.Get(ts.URL + "/v2/manifests/v1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bucketless path: status %d, want 404", resp.StatusCode)
	}
}

// TestSingleBucketModeIgnoresPathBucket guards the CLI's addressing: with Bucket
// set, the first segment is part of the image name, not a bucket.
func TestSingleBucketModeIgnoresPathBucket(t *testing.T) {
	b := newFakeBackend()
	b.set("fixed", "manifests/team/myapp/v1/manifest.json", []byte(testManifest))

	ts := httptest.NewServer(&Server{Client: b, Bucket: "fixed", PresignTTL: time.Hour})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v2/team/myapp/manifests/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

// TestCacheServesSecondRequestWithoutStorage is the reason the cache exists: a
// node pulling the same tag repeatedly should stop touching object storage.
func TestCacheServesSecondRequestWithoutStorage(t *testing.T) {
	b := newFakeBackend()
	b.set("testbucket", "manifests/myapp/v1/manifest.json", []byte(testManifest))

	obs := newCountingObserver()
	ts := httptest.NewServer(&Server{
		Client:   b,
		Bucket:   "testbucket",
		Cache:    NewCache(10, time.Minute),
		Observer: obs,
	})
	defer ts.Close()

	for i := 0; i < 3; i++ {
		resp, err := http.Get(ts.URL + "/v2/myapp/manifests/v1")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
	}

	if got := obs.count(obs.storage, "manifest_get"); got != 1 {
		t.Errorf("storage was read %d times for 3 requests, want 1", got)
	}
	if got := obs.count(obs.manifests, "cache"); got != 2 {
		t.Errorf("cache served %d requests, want 2", got)
	}
}

func TestCacheEvictsOldestBeyondMax(t *testing.T) {
	c := NewCache(2, 0)
	c.PutManifest("a", []byte("1"))
	c.PutManifest("b", []byte("2"))
	c.PutManifest("c", []byte("3"))

	if _, ok := c.Manifest("a"); ok {
		t.Error("oldest entry survived eviction")
	}
	if _, ok := c.Manifest("c"); !ok {
		t.Error("newest entry was evicted")
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2", c.Len())
	}
}

func TestCacheExpiresByTTL(t *testing.T) {
	c := NewCache(10, time.Nanosecond)
	c.PutManifest("a", []byte("1"))
	time.Sleep(time.Millisecond)
	if _, ok := c.Manifest("a"); ok {
		t.Error("entry outlived its TTL")
	}
}

func TestHealthAndReady(t *testing.T) {
	b := newFakeBackend()
	ts := httptest.NewServer(&Server{Client: b, Bucket: "testbucket"})
	defer ts.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, resp.StatusCode)
		}
	}
}

// TestWritesAreRejected pins the read-only contract: images are published with
// `s3lo push`, never through this endpoint.
func TestWritesAreRejected(t *testing.T) {
	b := newFakeBackend()
	ts := httptest.NewServer(&Server{Client: b, Bucket: "testbucket"})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v2/myapp/blobs/uploads/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status %d, want 405", resp.StatusCode)
	}
}

// TestDiskCacheSurvivesRestart covers what the in-memory tier cannot: a proxy
// that restarts with its pod should not have to refill from object storage.
func TestDiskCacheSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	first := NewDiskCache(10, time.Minute, dir)
	first.PutManifest("bucket/myapp/v1", []byte(testManifest))

	// A brand new cache, as after a restart: nothing in memory, everything on disk.
	second := NewDiskCache(10, time.Minute, dir)
	if second.Len() != 0 {
		t.Fatalf("fresh cache started with %d entries in memory", second.Len())
	}
	data, ok := second.Manifest("bucket/myapp/v1")
	if !ok {
		t.Fatal("manifest did not survive on disk")
	}
	if string(data) != testManifest {
		t.Error("manifest read back from disk does not match")
	}
	if second.Len() != 1 {
		t.Error("disk hit did not warm the in-memory tier")
	}
}

// TestDiskCacheKeyCannotEscapeDir guards the path handling: image names arrive
// from the network and contain slashes.
func TestDiskCacheKeyCannotEscapeDir(t *testing.T) {
	dir := t.TempDir()
	c := NewDiskCache(10, time.Minute, dir)
	c.PutManifest("../../etc/passwd", []byte("x"))

	got := c.diskPath("../../etc/passwd")
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("cache path %q escaped %q", got, dir)
	}
}
