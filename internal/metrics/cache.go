package metrics

import (
	"sync"
	"time"
)

// cacheKey isolates metrics by archive and extract preference so index-first
// and prefer_mount results do not clobber each other under the same TTL.
type cacheKey struct {
	archiveID   string
	preferMount bool
}

// Cache is a simple TTL cache for ArchiveMetrics keyed by
// (archive_id, prefer_mount) (parity with MetricsCache dual-path TTL).
// Get, Put, and Invalidate are safe for concurrent use.
type Cache struct {
	mu sync.RWMutex

	TTL time.Duration
	now func() time.Time

	entries map[cacheKey]cacheEntry
}

type cacheEntry struct {
	at    time.Time
	value ArchiveMetrics
}

// NewCache creates a TTL cache. ttl <= 0 means entries expire immediately
// (Get always misses unless put in the same instant with zero TTL check:
// zero TTL is treated as always-expired on Get).
func NewCache(ttlSeconds float64) *Cache {
	return &Cache{
		TTL:     time.Duration(ttlSeconds * float64(time.Second)),
		now:     time.Now,
		entries: make(map[cacheKey]cacheEntry),
	}
}

// SetClock injects a clock for tests. nil restores time.Now.
func (c *Cache) SetClock(now func() time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now == nil {
		c.now = time.Now
		return
	}
	c.now = now
}

// clock returns the current time. Caller must hold c.mu (R or W).
func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Get returns a cached metrics value if present and not expired for the
// given archive_id and prefer_mount preference.
func (c *Cache) Get(archiveID string, preferMount bool) (ArchiveMetrics, bool) {
	if c == nil {
		return ArchiveMetrics{}, false
	}
	k := cacheKey{archiveID: archiveID, preferMount: preferMount}

	c.mu.RLock()
	if c.entries == nil {
		c.mu.RUnlock()
		return ArchiveMetrics{}, false
	}
	e, ok := c.entries[k]
	if !ok {
		c.mu.RUnlock()
		return ArchiveMetrics{}, false
	}
	expired := c.TTL <= 0 || c.clock().Sub(e.at) > c.TTL
	if !expired {
		val := e.value
		c.mu.RUnlock()
		return val, true
	}
	c.mu.RUnlock()

	// Expired: re-check under write lock and delete if still stale.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return ArchiveMetrics{}, false
	}
	e, ok = c.entries[k]
	if !ok {
		return ArchiveMetrics{}, false
	}
	if c.TTL <= 0 || c.clock().Sub(e.at) > c.TTL {
		delete(c.entries, k)
		return ArchiveMetrics{}, false
	}
	return e.value, true
}

// Put stores metrics under (metrics.ArchiveID, preferMount).
func (c *Cache) Put(m ArchiveMetrics, preferMount bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[cacheKey]cacheEntry)
	}
	k := cacheKey{archiveID: m.ArchiveID, preferMount: preferMount}
	c.entries[k] = cacheEntry{at: c.clock(), value: m}
}

// Invalidate removes all prefer_mount variants for one archive, or clears
// the entire cache when archiveID is "".
func (c *Cache) Invalidate(archiveID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return
	}
	if archiveID == "" {
		c.entries = make(map[cacheKey]cacheEntry)
		return
	}
	for k := range c.entries {
		if k.archiveID == archiveID {
			delete(c.entries, k)
		}
	}
}
