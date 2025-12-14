package cache

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/mirkobrombin/go-warp/v1/cache")

// Cache defines the basic operations for a cache layer.
//
// T represents the type of values stored in the cache.
type Cache[T any] interface {
	// Get retrieves a value for the given key. The boolean return
	// indicates whether the key was found. An error is returned if
	// retrieving the value fails.
	Get(ctx context.Context, key string) (T, bool, error)
	// Set stores the value for the given key for the specified TTL.
	Set(ctx context.Context, key string, value T, ttl time.Duration) error
	// Invalidate removes the key from the cache.
	Invalidate(ctx context.Context, key string) error
}

// InMemoryCache is a simple in-memory cache implementation with TTL support.
type InMemoryCache[T any] struct {
	mu            sync.RWMutex
	items         map[string]item[T]
	order         *list.List
	hits          atomic.Uint64
	misses        atomic.Uint64
	sweepInterval time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	maxEntries    int

	hitCounter      prometheus.Counter
	missCounter     prometheus.Counter
	evictionCounter prometheus.Counter
	latencyHist     prometheus.Histogram
	traceEnabled    bool
}

type item[T any] struct {
	value     T
	expiresAt time.Time
	element   *list.Element
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

// WithMaxEntries sets the maximum number of entries the cache can hold.
// A non-positive value means the cache size is unbounded.
func WithMaxEntries[T any](n int) InMemoryOption[T] {
	return func(c *InMemoryCache[T]) {
		c.maxEntries = n
	}
}

// WithMetrics enables Prometheus metrics collection using the provided registerer.
func WithMetrics[T any](reg prometheus.Registerer) InMemoryOption[T] {
	return func(c *InMemoryCache[T]) {
		c.hitCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_cache_hits_total",
			Help: "Total number of cache hits",
		})
		c.missCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_cache_misses_total",
			Help: "Total number of cache misses",
		})
		c.evictionCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_cache_evictions_total",
			Help: "Total number of cache evictions",
		})
		c.latencyHist = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "warp_cache_latency_seconds",
			Help:    "Latency of cache operations",
			Buckets: prometheus.DefBuckets,
		})
		reg.MustRegister(c.hitCounter, c.missCounter, c.evictionCounter, c.latencyHist)
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
		order:         list.New(),
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
func (c *InMemoryCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var span trace.Span
	var start time.Time
	if c.traceEnabled {
		ctx, span = tracer.Start(ctx, "Cache.Get")
		defer span.End()
		start = time.Now()
	} else if c.latencyHist != nil {
		start = time.Now()
	}

	if c.traceEnabled || c.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if c.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.cache.latency_ms", latency.Milliseconds()))
			}
			if c.latencyHist != nil {
				c.latencyHist.Observe(latency.Seconds())
			}
		}()
	}
	select {
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	default:
	}
	c.mu.Lock()
	it, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		if c.missCounter != nil {
			c.missCounter.Inc()
		}
		if c.traceEnabled {
			span.SetAttributes(attribute.String("warp.cache.result", "miss"))
		}
		var zero T
		return zero, false, nil
	}
	if !it.expiresAt.IsZero() && time.Now().After(it.expiresAt) {
		// remove expired item
		c.order.Remove(it.element)
		delete(c.items, key)
		c.mu.Unlock()
		c.misses.Add(1)
		if c.missCounter != nil {
			c.missCounter.Inc()
		}
		if c.evictionCounter != nil {
			c.evictionCounter.Inc()
		}
		if c.traceEnabled {
			span.SetAttributes(attribute.String("warp.cache.result", "miss"))
		}
		var zero T
		return zero, false, nil
	}
	// mark as recently used
	c.order.MoveToFront(it.element)
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	default:
	}
	c.hits.Add(1)
	if c.hitCounter != nil {
		c.hitCounter.Inc()
	}
	if c.traceEnabled {
		span.SetAttributes(attribute.String("warp.cache.result", "hit"))
	}
	return it.value, true, nil
}

// Set implements Cache.Set.
func (c *InMemoryCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	var span trace.Span
	var start time.Time
	if c.traceEnabled {
		ctx, span = tracer.Start(ctx, "Cache.Set")
		defer span.End()
		start = time.Now()
	} else if c.latencyHist != nil {
		start = time.Now()
	}

	if c.traceEnabled || c.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if c.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.cache.latency_ms", latency.Milliseconds()))
			}
			if c.latencyHist != nil {
				c.latencyHist.Observe(latency.Seconds())
			}
		}()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if it, ok := c.items[key]; ok {
		it.value = value
		it.expiresAt = exp
		c.items[key] = it
		c.order.MoveToFront(it.element)
	} else {
		elem := c.order.PushFront(key)
		c.items[key] = item[T]{value: value, expiresAt: exp, element: elem}
		if c.maxEntries > 0 && len(c.items) > c.maxEntries {
			tail := c.order.Back()
			if tail != nil {
				k := tail.Value.(string)
				c.order.Remove(tail)
				delete(c.items, k)
				if c.evictionCounter != nil {
					c.evictionCounter.Inc()
				}
			}
		}
	}
	return nil
}

// Invalidate implements Cache.Invalidate.
func (c *InMemoryCache[T]) Invalidate(ctx context.Context, key string) error {
	var span trace.Span
	var start time.Time
	if c.traceEnabled {
		ctx, span = tracer.Start(ctx, "Cache.Invalidate")
		defer span.End()
		start = time.Now()
	} else if c.latencyHist != nil {
		start = time.Now()
	}

	if c.traceEnabled || c.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if c.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.cache.latency_ms", latency.Milliseconds()))
			}
			if c.latencyHist != nil {
				c.latencyHist.Observe(latency.Seconds())
			}
		}()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if it, ok := c.items[key]; ok {
		c.order.Remove(it.element)
		delete(c.items, key)
		if c.evictionCounter != nil {
			c.evictionCounter.Inc()
		}
	}
	return nil
}

// sweeper periodically removes expired items from the cache.
// It uses a probabilistic approach similar to Redis *to avoid locking the map for too long.
// * At least it tries...
func (c *InMemoryCache[T]) sweeper() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.sweepInterval)
	defer ticker.Stop()

	// Constants for the probabilistic expiration algorithm
	const (
		sampleSize    = 20
		evictionRatio = 0.25 // If > 25% of sample is expired, repeat
	)

	for {
		select {
		case <-ticker.C:
			for {
				expiredCount := 0
				checkedCount := 0
				now := time.Now()

				c.mu.Lock()
				// If cache is empty, nothing to do
				if len(c.items) == 0 {
					c.mu.Unlock()
					break
				}

				for k, it := range c.items {
					checkedCount++
					if !it.expiresAt.IsZero() && now.After(it.expiresAt) {
						c.order.Remove(it.element)
						delete(c.items, k)
						if c.evictionCounter != nil {
							c.evictionCounter.Inc()
						}
						expiredCount++
					}
					// Stop after checking sampleSize
					if checkedCount >= sampleSize {
						break
					}
				}
				c.mu.Unlock()

				// If we expired fewer than the ratio, we assume most keys are valid and stop.
				// Otherwise, we repeat immediately to aggressively clean up.
				if float64(expiredCount) < float64(sampleSize)*evictionRatio {
					break
				}
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// Close terminates any background goroutines used by the cache.
func (c *InMemoryCache[T]) Close() {
	c.cancel()
	c.wg.Wait()
	c.mu.Lock()
	c.items = make(map[string]item[T])
	c.order.Init()
	c.mu.Unlock()
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
		Hits:   c.hits.Load(),
		Misses: c.misses.Load(),
		Size:   size,
	}
}

// WithTracing enables OpenTelemetry tracing for cache operations.
func WithTracing[T any]() InMemoryOption[T] {
	return func(c *InMemoryCache[T]) {
		c.traceEnabled = true
	}
}
