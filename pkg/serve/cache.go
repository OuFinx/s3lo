package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache holds recently served manifests so repeated pulls of the same tag do not
// re-read object storage. Only manifests are cached: they are small, they are
// fetched on every single pull, and they are the one object a pull cannot avoid.
// Blobs are deliberately absent — they are orders of magnitude larger and are
// already served either by redirect or by streaming.
//
// Setting Dir adds best-effort disk persistence so a restarted process does not
// have to refill the cache from object storage, which matters for a node-local
// proxy that restarts with its pod.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	nextSeq uint64 // insertion counter, for FIFO eviction
	max     int
	ttl     time.Duration
	dir     string
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
	// seq is the entry's place in insertion order. It lives on the entry rather
	// than in a parallel slice because the slice went out of sync: TTL expiry
	// deleted from the map only, the next Put re-appended the same key, and
	// eviction (which measured the slice) started dropping live entries. The
	// cache's effective capacity decayed silently below max.
	seq uint64
}

// NewCache returns an in-memory cache holding at most max manifests for ttl
// each. A max of zero means unlimited; a ttl of zero means entries never expire.
func NewCache(max int, ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]cacheEntry),
		max:     max,
		ttl:     ttl,
	}
}

// NewDiskCache is NewCache plus best-effort persistence under dir. Disk errors
// are ignored throughout: a cache that fails to persist must still serve.
func NewDiskCache(max int, ttl time.Duration, dir string) *Cache {
	c := NewCache(max, ttl)
	c.dir = dir
	return c
}

// diskPath maps a cache key to a file name by hashing it. Keys contain slashes
// and arbitrary tag text, so hashing is both the escaping strategy and the
// guarantee that a crafted image name cannot walk out of the cache directory.
func (c *Cache) diskPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, "manifests", hex.EncodeToString(sum[:]))
}

func (c *Cache) readDisk(key string) ([]byte, bool) {
	path := c.diskPath(key)
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if c.ttl > 0 && time.Since(info.ModTime()) > c.ttl {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func (c *Cache) writeDisk(key string, data []byte) {
	path := c.diskPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// Manifest returns a cached manifest, if present and unexpired.
func (c *Cache) Manifest(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		if c.dir == "" {
			return nil, false
		}
		data, found := c.readDisk(key)
		if !found {
			return nil, false
		}
		c.PutManifest(key, data) // warm the in-memory tier
		return data, true
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}
	return e.data, true
}

// PutManifest stores a manifest, evicting the oldest entry when full.
func (c *Cache) PutManifest(key string, data []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	seq := c.nextSeq
	if old, exists := c.entries[key]; exists {
		seq = old.seq // a refresh keeps its place in the queue
	} else {
		c.nextSeq++
	}

	var expires time.Time
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}
	c.entries[key] = cacheEntry{data: data, expiresAt: expires, seq: seq}

	// ponytail: linear scan for the oldest entry, bounded by max (1000 by
	// default) and only on a full cache. Swap in a heap if max ever gets large.
	for c.max > 0 && len(c.entries) > c.max {
		oldestKey, oldestSeq := "", uint64(0)
		for k, e := range c.entries {
			if oldestKey == "" || e.seq < oldestSeq {
				oldestKey, oldestSeq = k, e.seq
			}
		}
		delete(c.entries, oldestKey)
	}

	if c.dir != "" {
		c.writeDisk(key, data)
	}
}

// Len reports how many manifests are currently held.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
