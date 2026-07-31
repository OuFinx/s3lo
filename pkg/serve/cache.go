package serve

import (
	"sync"
	"time"
)

// Cache holds recently served manifests so repeated pulls of the same tag do not
// re-read object storage. Only manifests are cached: they are small, they are
// fetched on every single pull, and they are the one object a pull cannot avoid.
// Blobs are deliberately absent — they are orders of magnitude larger and are
// already served either by redirect or by streaming.
//
// ponytail: in-memory only. A disk cache would survive a restart, but manifests
// are kilobytes and refetching them costs one GET; add persistence only if
// restart storms show up as real object-storage load.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	order   []string // insertion order, for FIFO eviction
	max     int
	ttl     time.Duration
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// NewCache returns a cache holding at most max manifests for ttl each.
// A max of zero means unlimited; a ttl of zero means entries never expire.
func NewCache(max int, ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]cacheEntry),
		max:     max,
		ttl:     ttl,
	}
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
		return nil, false
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

	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
		for c.max > 0 && len(c.order) > c.max {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
	}

	var expires time.Time
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}
	c.entries[key] = cacheEntry{data: data, expiresAt: expires}
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
