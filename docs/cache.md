# Cache Module

The `cache` module provides the L1 caching layer for Warp.

## API Reference

### `NewInMemory`

Creates a new in-memory cache.

```go
func NewInMemory[T any](opts ...InMemoryOption[T]) *InMemoryCache[T]
```

#### Options

- **`WithMaxEntries(n int)`**: Sets a hard limit on the number of items. Evicts LRU when full.
- **`WithSweepInterval(d time.Duration)`**: Sets how often the background sweeper runs to remove expired items. Default is 1 minute.
- **`WithMetrics(reg prometheus.Registerer)`**: Registers Prometheus metrics.

### `NewResilient`

Wraps an existing `Cache` implementation with **Cache Resiliency**. This decorator suppresses errors from the underlying cache operations (Get, Set, Invalidate), logs them as warnings, and ensures application stability by treating cache failures as cache misses or successful (but skipped) writes.

```go
func NewResilient[T any](inner Cache[T]) *ResilientCache[T]
```

### `Close`

```go
func (c *InMemoryCache[T]) Close()
```
Terminates any background goroutines (e.g., sweeper) used by the cache.

### `Metrics`

```go
func (c *InMemoryCache[T]) Metrics() Stats
```
Returns current usage statistics.

```go
type Stats struct {
    Hits   uint64
    Misses uint64
    Size   int
}
```

### `NewAdaptiveTTLStrategy`

Creates a TTL strategy that adjusts based on access frequency.

```go
func NewAdaptiveTTLStrategy(min, max time.Duration, factor float64) *AdaptiveTTLStrategy
```

- **min**: Minimum TTL.
- **max**: Maximum TTL.
- **factor**: Multiplier applied on each hit (e.g., 1.5 = +50%).

### `Cache` Interface

To implement a custom backend (e.g. Redis L1), implement this interface:

```go
type Cache[T any] interface {
    Get(ctx context.Context, key string) (T, bool, error)
    Set(ctx context.Context, key string, value T, ttl time.Duration) error
    Invalidate(ctx context.Context, key string) error
}
```

## Features

### Advanced TTL Options (`cache.TTLOption`)

These options can be passed to `Warp.Register` and `Warp.RegisterDynamicTTL`.

- **`WithSliding()`**: Resets the TTL to its original value on every access.
- **`WithFailSafe(grace time.Duration)`**: Enables the **Fail-Safe** (Stale-If-Error) pattern. If the backend fails, the cache will return the expired value if it is within the specified grace period, improving resilience.
- **`WithSoftTimeout(d time.Duration)`**: Sets a **Soft Timeout** for backend fetch operations. If the backend takes longer than the duration, the cache returns the stale value (if available) instead of waiting or failing, protecting latency.
- **`WithEagerRefresh(threshold float64)`**: Enables **Eager Refresh**. If an item's remaining TTL falls below the specified threshold (e.g., `0.1` for 10%), a refresh is triggered in the background while serving the current item, ensuring data is always fresh for users. The threshold must be between `0.0` and `1.0`.

### Adaptive TTL

One of Warp's unique features is **Adaptive TTL**. Instead of a fixed expiration time, the TTL can adjust dynamically based on access patterns.

```go
// Min: 1m, Max: 1h, Growth: 1.5x
strategy := cache.NewAdaptiveTTLStrategy(time.Minute, time.Hour, 1.5)
w.RegisterDynamicTTL("product:*", core.ModeEventualDistributed, strategy)
```
