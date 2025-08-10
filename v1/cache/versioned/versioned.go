package versioned

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

// Cache wraps a cache.Cache of VersionedValue to keep multiple versions per key.
// It exposes the cache.Cache interface for merge.Value while maintaining
// up to `limit` historical versions per key.
type Cache[T any] struct {
	base       cache.Cache[merge.VersionedValue[T]]
	limit      int
	mu         sync.Mutex
	order      *list.List
	entries    map[string]*list.Element
	maxEntries int
	hits       uint64
	misses     uint64
	evictions  uint64

	hitCounter      prometheus.Counter
	missCounter     prometheus.Counter
	evictionCounter prometheus.Counter
}

// Option configures the versioned cache.
type Option[T any] func(*Cache[T])

// WithMaxEntries sets a global LRU limit on the number of keys stored.
func WithMaxEntries[T any](n int) Option[T] {
	return func(c *Cache[T]) { c.maxEntries = n }
}

// WithMetrics enables Prometheus metrics collection using the provided registerer.
func WithMetrics[T any](reg prometheus.Registerer) Option[T] {
	return func(c *Cache[T]) {
		c.hitCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_versioned_cache_hits_total",
			Help: "Total number of versioned cache hits",
		})
		c.missCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_versioned_cache_misses_total",
			Help: "Total number of versioned cache misses",
		})
		c.evictionCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_versioned_cache_evictions_total",
			Help: "Total number of versioned cache evictions",
		})
		reg.MustRegister(c.hitCounter, c.missCounter, c.evictionCounter)
	}
}

// New creates a new versioned cache with the provided base cache and history limit.
func New[T any](base cache.Cache[merge.VersionedValue[T]], limit int, opts ...Option[T]) *Cache[T] {
	c := &Cache[T]{
		base:    base,
		limit:   limit,
		order:   list.New(),
		entries: make(map[string]*list.Element),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get returns the most recent value for a key.
func (c *Cache[T]) Get(ctx context.Context, key string) (merge.Value[T], bool, error) {
	vv, ok, err := c.base.Get(ctx, key)
	if err != nil || !ok {
		if err == nil && !ok {
			atomic.AddUint64(&c.misses, 1)
			if c.missCounter != nil {
				c.missCounter.Inc()
			}
		}
		var zero merge.Value[T]
		return zero, ok, err
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.order.MoveToFront(e)
	}
	c.mu.Unlock()
	atomic.AddUint64(&c.hits, 1)
	if c.hitCounter != nil {
		c.hitCounter.Inc()
	}
	v, vok := vv.Latest()
	if !vok {
		atomic.AddUint64(&c.misses, 1)
		if c.missCounter != nil {
			c.missCounter.Inc()
		}
	}
	return v, vok, nil
}

// GetAt returns the value associated with the key at the specified time.
func (c *Cache[T]) GetAt(ctx context.Context, key string, at time.Time) (merge.Value[T], bool, error) {
	vv, ok, err := c.base.Get(ctx, key)
	if err != nil || !ok {
		if err == nil && !ok {
			atomic.AddUint64(&c.misses, 1)
			if c.missCounter != nil {
				c.missCounter.Inc()
			}
		}
		var zero merge.Value[T]
		return zero, ok, err
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.order.MoveToFront(e)
	}
	c.mu.Unlock()
	v, vok := vv.At(at)
	if vok {
		atomic.AddUint64(&c.hits, 1)
		if c.hitCounter != nil {
			c.hitCounter.Inc()
		}
	} else {
		atomic.AddUint64(&c.misses, 1)
		if c.missCounter != nil {
			c.missCounter.Inc()
		}
	}
	return v, vok, nil
}

// Set stores a new value for the key and records the history.
func (c *Cache[T]) Set(ctx context.Context, key string, val merge.Value[T], ttl time.Duration) error {
	vv, ok, err := c.base.Get(ctx, key)
	if err != nil {
		return err
	}
	if !ok {
		vv = merge.NewVersionedValue(val, c.limit)
	} else {
		vv.Add(val)
	}
	if err := c.base.Set(ctx, key, vv, ttl); err != nil {
		return err
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.order.MoveToFront(e)
	} else {
		e := c.order.PushFront(key)
		c.entries[key] = e
		if c.maxEntries > 0 && c.order.Len() > c.maxEntries {
			last := c.order.Back()
			if last != nil {
				k := last.Value.(string)
				c.order.Remove(last)
				delete(c.entries, k)
				_ = c.base.Invalidate(context.Background(), k)
				atomic.AddUint64(&c.evictions, 1)
				if c.evictionCounter != nil {
					c.evictionCounter.Inc()
				}
			}
		}
	}
	c.mu.Unlock()
	return nil
}

// Invalidate removes all versions of the key.
func (c *Cache[T]) Invalidate(ctx context.Context, key string) error {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.order.Remove(e)
		delete(c.entries, key)
	}
	c.mu.Unlock()
	return c.base.Invalidate(ctx, key)
}

// Metrics exposes cache statistics.
type Metrics struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
}

// Metrics returns the collected metrics.
func (c *Cache[T]) Metrics() Metrics {
	return Metrics{
		Hits:      atomic.LoadUint64(&c.hits),
		Misses:    atomic.LoadUint64(&c.misses),
		Evictions: atomic.LoadUint64(&c.evictions),
	}
}

// ensure Cache implements cache.Cache for merge.Value
var _ cache.Cache[merge.Value[struct{}]] = (*Cache[struct{}])(nil)
