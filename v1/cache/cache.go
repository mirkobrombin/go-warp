package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Cache defines the basic operations for a cache layer.
type Cache interface {
	// Get retrieves a value for the given key. The boolean return
	// indicates whether the key was found.
	Get(ctx context.Context, key string) (any, bool)
	// Set stores the value for the given key for the specified TTL.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	// Invalidate removes the key from the cache.
	Invalidate(ctx context.Context, key string) error
}

// InMemoryCache is a simple in-memory cache implementation with TTL support.
type InMemoryCache struct {
	mu            sync.RWMutex
	items         map[string]item
	hits          uint64
	misses        uint64
	sweepInterval time.Duration
}

type item struct {
	value     any
	expiresAt time.Time
}

// InMemoryOption configures an InMemoryCache.
type InMemoryOption func(*InMemoryCache)

// WithSweepInterval sets the interval at which expired items are removed.
// A zero or negative duration disables the background sweeper.
func WithSweepInterval(d time.Duration) InMemoryOption {
	return func(c *InMemoryCache) {
		c.sweepInterval = d
	}
}

// defaultSweepInterval is the default period for removing expired items.
// The value is chosen to balance timely cleanup with minimal overhead.
const defaultSweepInterval = time.Minute

// NewInMemory returns a new InMemoryCache instance.
//
// An optional sweep interval can be provided using WithSweepInterval. When
// enabled, a background goroutine periodically removes expired items from the
// cache. The default interval is one minute.
func NewInMemory(opts ...InMemoryOption) *InMemoryCache {
	c := &InMemoryCache{
		items:         make(map[string]item),
		sweepInterval: defaultSweepInterval,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.sweepInterval > 0 {
		go c.sweeper()
	}
	return c
}

// Get implements Cache.Get.
func (c *InMemoryCache) Get(ctx context.Context, key string) (any, bool) {
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		atomic.AddUint64(&c.misses, 1)
		return nil, false
	}
	if !it.expiresAt.IsZero() && time.Now().After(it.expiresAt) {
		_ = c.Invalidate(ctx, key)
		atomic.AddUint64(&c.misses, 1)
		return nil, false
	}
	atomic.AddUint64(&c.hits, 1)
	return it.value, true
}

// Set implements Cache.Set.
func (c *InMemoryCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.items[key] = item{value: value, expiresAt: exp}
	c.mu.Unlock()
	return nil
}

// Invalidate implements Cache.Invalidate.
func (c *InMemoryCache) Invalidate(ctx context.Context, key string) error {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

// sweeper periodically removes expired items from the cache.
func (c *InMemoryCache) sweeper() {
	ticker := time.NewTicker(c.sweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for k, it := range c.items {
			if !it.expiresAt.IsZero() && now.After(it.expiresAt) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

// Stats reports basic metrics about cache usage.
type Stats struct {
	Hits   uint64
	Misses uint64
	Size   int
}

// Metrics returns current metrics for the cache.
func (c *InMemoryCache) Metrics() Stats {
	c.mu.RLock()
	size := len(c.items)
	c.mu.RUnlock()
	return Stats{
		Hits:   atomic.LoadUint64(&c.hits),
		Misses: atomic.LoadUint64(&c.misses),
		Size:   size,
	}
}
