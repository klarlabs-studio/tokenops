// Package cache hosts the response cache the proxy uses to short-circuit
// repeat completions. Keys are SHA-256 hashes of (provider, method, path,
// request body); values are status + headers + body buffered up to a
// per-entry byte cap. Streaming responses are never cached — SSE replay
// from a buffered store would deliver every chunk in a single flush, so
// the proxy explicitly bypasses caching when Content-Type is event-stream.
//
// Eviction is LRU + TTL + total-size: any of expired, beyond MaxEntries,
// or beyond MaxBytes evicts the least-recently-used entry. The cache is
// safe for concurrent use; all mutations take the package-private mutex.
package cache

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

// Options tune the cache. Zero values fall back to sensible defaults
// (1024 entries, 64MB total, 10-minute TTL, 4MB per-entry body cap).
type Options struct {
	// MaxEntries caps the number of cached responses. Default 1024.
	MaxEntries int
	// MaxBytes caps the total bytes of cached response bodies. Default 64MB.
	MaxBytes int64
	// MaxEntryBytes caps a single cached body. Larger responses skip the
	// cache. Default 4MB.
	MaxEntryBytes int64
	// TTL is the time after which an entry is considered stale and is
	// evicted on the next access. Default 10 minutes.
	TTL time.Duration
	// Now overrides the wall-clock used for TTL bookkeeping (tests).
	Now func() time.Time
}

// Entry is a cached response. Headers is shallow-copied at insert time;
// callers must not mutate the slices after Put.
type Entry struct {
	Status      int
	Headers     map[string][]string
	Body        []byte
	StoredAt    time.Time
	ContentType string
}

// Metrics is a snapshot of the cache counters. Read with Metrics().
type Metrics struct {
	Hits      int64 `json:"hits"`
	Misses    int64 `json:"misses"`
	Stores    int64 `json:"stores"`
	Bypasses  int64 `json:"bypasses"`
	Evictions int64 `json:"evictions"`
	Entries   int64 `json:"entries"`
	Bytes     int64 `json:"bytes"`
}

// Cache is an LRU + TTL response cache.
type Cache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List

	maxEntries    int
	maxBytes      int64
	maxEntryBytes int64
	ttl           time.Duration
	now           func() time.Time
	bytes         int64

	hits      atomic.Int64
	misses    atomic.Int64
	stores    atomic.Int64
	bypasses  atomic.Int64
	evictions atomic.Int64
}

type lruItem struct {
	key   string
	entry *Entry
}

// New constructs a Cache with opts. Sensible defaults are filled in for
// any zero-valued field.
func New(opts Options) *Cache {
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = 1024
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 64 * 1024 * 1024
	}
	if opts.MaxEntryBytes <= 0 {
		opts.MaxEntryBytes = 4 * 1024 * 1024
	}
	if opts.TTL <= 0 {
		opts.TTL = 10 * time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Cache{
		entries:       make(map[string]*list.Element),
		lru:           list.New(),
		maxEntries:    opts.MaxEntries,
		maxBytes:      opts.MaxBytes,
		maxEntryBytes: opts.MaxEntryBytes,
		ttl:           opts.TTL,
		now:           opts.Now,
	}
}

// Get returns the cached entry for key when present and not expired.
// Hits move the entry to the front of the LRU list.
func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	item := elem.Value.(*lruItem)
	if c.now().Sub(item.entry.StoredAt) > c.ttl {
		c.removeElement(elem)
		c.misses.Add(1)
		return nil, false
	}
	c.lru.MoveToFront(elem)
	c.hits.Add(1)
	// Return a defensive copy of the body so concurrent writers can not
	// mutate a streaming response in flight.
	body := append([]byte(nil), item.entry.Body...)
	headers := cloneHeaders(item.entry.Headers)
	out := *item.entry
	out.Body = body
	out.Headers = headers
	return &out, true
}

// Put stores entry under key. Bodies larger than MaxEntryBytes are
// skipped (returns false). Putting an existing key updates the value
// and refreshes its position in the LRU.
func (c *Cache) Put(key string, entry *Entry) bool {
	if entry == nil {
		return false
	}
	if int64(len(entry.Body)) > c.maxEntryBytes {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.StoredAt.IsZero() {
		entry.StoredAt = c.now()
	}
	if elem, ok := c.entries[key]; ok {
		old := elem.Value.(*lruItem)
		c.bytes -= int64(len(old.entry.Body))
		old.entry = entry
		c.lru.MoveToFront(elem)
		c.bytes += int64(len(entry.Body))
	} else {
		item := &lruItem{key: key, entry: entry}
		elem := c.lru.PushFront(item)
		c.entries[key] = elem
		c.bytes += int64(len(entry.Body))
	}
	c.stores.Add(1)
	c.evictWhileOverLimit()
	return true
}

// MarkBypass records a bypass for metrics reporting. The cache itself is
// untouched.
func (c *Cache) MarkBypass() { c.bypasses.Add(1) }

// Delete removes key from the cache. Useful for cache-control: refresh
// flows where the proxy wants to drop a stale entry on demand.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.removeElement(elem)
	}
}

// Purge drops every entry. Counters are preserved.
func (c *Cache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.lru.Init()
	c.bytes = 0
}

// Len returns the number of resident entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Metrics returns a snapshot. The returned struct is a value copy; safe
// to read concurrently with cache mutations.
func (c *Cache) Metrics() Metrics {
	c.mu.Lock()
	entries := int64(len(c.entries))
	bytes := c.bytes
	c.mu.Unlock()
	return Metrics{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Stores:    c.stores.Load(),
		Bypasses:  c.bypasses.Load(),
		Evictions: c.evictions.Load(),
		Entries:   entries,
		Bytes:     bytes,
	}
}

// --- internals ----------------------------------------------------------

func (c *Cache) evictWhileOverLimit() {
	for c.lru.Len() > c.maxEntries || c.bytes > c.maxBytes {
		oldest := c.lru.Back()
		if oldest == nil {
			return
		}
		c.removeElement(oldest)
	}
}

func (c *Cache) removeElement(elem *list.Element) {
	item := elem.Value.(*lruItem)
	delete(c.entries, item.key)
	c.lru.Remove(elem)
	c.bytes -= int64(len(item.entry.Body))
	c.evictions.Add(1)
}

func cloneHeaders(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		dup := make([]string, len(v))
		copy(dup, v)
		out[k] = dup
	}
	return out
}
