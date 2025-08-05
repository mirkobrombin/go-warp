package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Cache defines the basic operations for a cache layer.
//
// T represents the type of values stored in the cache.
type Cache[T any] interface {
	// Get retrieves a value for the given key. The boolean return
	// indicates whether the key was found.
	Get(ctx context.Context, key string) (T, bool)
	// Set stores the value for the given key for the specified TTL.
	Set(ctx context.Context, key string, value T, ttl time.Duration) error
	// Invalidate removes the key from the cache.
	Invalidate(ctx context.Context, key string) error
}

// InMemoryCache is a simple in-memory cache implementation with TTL support.
type InMemoryCache[T any] struct {
	mu            sync.RWMutex
	items         map[string]item[T]
	hits          uint64
	misses        uint64
	sweepInterval time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

type item[T any] struct {
	value     T
	expiresAt time.Time
}

// InMemoryOption configures an InMemoryCache.
type InMemoryOption[T any] func(*InMemoryCache[T])

// WithSweepInterval sets the interval at which expired items are removed.
// A zero or negative duration disables the background sweeper.
func WithSweepInterval[T any](d time.Duration) InMemoryOption[T] {
	return func(c *InMemoryCache[T]) {
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
func NewInMemory[T any](opts ...InMemoryOption[T]) *InMemoryCache[T] {
	ctx, cancel := context.WithCancel(context.Background())
	c := &InMemoryCache[T]{
		items:         make(map[string]item[T]),
		sweepInterval: defaultSweepInterval,
		ctx:           ctx,
		cancel:        cancel,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.sweepInterval > 0 {
		c.wg.Add(1)
		go c.sweeper()
	}
	return c
}

// Get implements Cache.Get.
func (c *InMemoryCache[T]) Get(ctx context.Context, key string) (T, bool) {
	c.mu.RLock()
	it, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		atomic.AddUint64(&c.misses, 1)
		var zero T
		return zero, false
	}
	if !it.expiresAt.IsZero() && time.Now().After(it.expiresAt) {
		_ = c.Invalidate(ctx, key)
		atomic.AddUint64(&c.misses, 1)
		var zero T
		return zero, false
	}
	atomic.AddUint64(&c.hits, 1)
	return it.value, true
}

// Set implements Cache.Set.
func (c *InMemoryCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.items[key] = item[T]{value: value, expiresAt: exp}
	c.mu.Unlock()
	return nil
}

// Invalidate implements Cache.Invalidate.
func (c *InMemoryCache[T]) Invalidate(ctx context.Context, key string) error {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

// sweeper periodically removes expired items from the cache.
func (c *InMemoryCache[T]) sweeper() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for k, it := range c.items {
				if !it.expiresAt.IsZero() && now.After(it.expiresAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		case <-c.ctx.Done():
			return
		}
	}
}

// Close terminates any background goroutines used by the cache.
func (c *InMemoryCache[T]) Close() {
	c.cancel()
	c.wg.Wait()
}

// Stats reports basic metrics about cache usage.
type Stats struct {
	Hits   uint64
	Misses uint64
	Size   int
}

// Metrics returns current metrics for the cache.
func (c *InMemoryCache[T]) Metrics() Stats {
	c.mu.RLock()
	size := len(c.items)
	c.mu.RUnlock()
	return Stats{
		Hits:   atomic.LoadUint64(&c.hits),
		Misses: atomic.LoadUint64(&c.misses),
		Size:   size,
	}
}
