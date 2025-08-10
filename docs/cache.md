# Cache Layer

The `cache` package defines the `Cache` interface and provides in-memory caches with multiple eviction strategies as well as a Redis implementation.

## Interface

```go
type Cache[T any] interface {
    Get(ctx context.Context, key string) (T, bool)
    Set(ctx context.Context, key string, value T, ttl time.Duration)
    Invalidate(ctx context.Context, key string)
}
```

## Strategies

`cache.New` constructs an in-memory cache. By default it uses an [LRU](glossary.md#lru) policy but a different strategy can be selected:

```go
c := cache.New[string]()                                 // LRU
c := cache.New[string](cache.WithStrategy[string](cache.LFUStrategy))
c := cache.New[string](cache.WithStrategy[string](cache.AdaptiveStrategy))
```

The strategies are:

* **[LRU](glossary.md#lru)** – least recently used, implemented by `LRUCache`.
* **[LFU/TinyLFU](glossary.md#lfu-tinylfu)** – least frequently used, backed by the [Ristretto](glossary.md#ristretto) library.
* **Adaptive** – dynamically switches between LRU and LFU based on the observed hit/miss ratio.

## LRU Cache

`LRUCache` is a simple map based cache with TTL support and basic metrics.
It also runs a background goroutine that periodically removes expired items.
The sweep happens every minute by default and can be customized:

```go
c := cache.NewLRU[string](cache.WithSweepInterval[string](30 * time.Second))
stats := c.Metrics() // Hits, Misses, Size
```

The sweeper adds a small amount of overhead as it iterates over cached items at
each interval. For most workloads this is negligible, but very large caches may
see increased CPU usage during sweeps.

You can also limit the number of items stored by providing `WithMaxEntries`. When
the cache grows beyond this limit the least recently used item is evicted:

```go
c := cache.NewLRU[string](cache.WithMaxEntries[string](100))
```

Calls to `Get` mark items as recently used, ensuring that frequently accessed
entries remain in the cache.

## LFU Cache

`LFUCache` builds on [dgraph-io/ristretto](https://github.com/dgraph-io/ristretto)
and offers a high performance in-memory cache:

```go
c := cache.NewLFU[string]()
```

You can supply your own configuration through `WithRistretto`:

```go
cfg := &ristretto.Config{NumCounters: 1e5, MaxCost: 1 << 20, BufferItems: 64}
c := cache.NewLFU[string](cache.WithRistretto(cfg))
```

## Adaptive Cache

`AdaptiveCache` maintains both LRU and LFU caches and periodically switches to the
strategy that yields more hits. It can be created with:

```go
c := cache.New[string](cache.WithStrategy[string](cache.AdaptiveStrategy))
```

## [Sliding and Dynamic TTL](glossary.md#sliding-ttl)

`Warp.Register` accepts additional options from the `cache` package to control
TTL behaviour. `WithSliding` resets the TTL every time a key is read, while
`WithDynamicTTL` adjusts the TTL based on access frequency.

```go
w.Register(
    "greeting",
    core.ModeStrongLocal,
    time.Minute,
    cache.WithSliding(),
    cache.WithDynamicTTL(30*time.Second, time.Minute, 30*time.Second, time.Minute, 10*time.Minute),
)
```

In the example above, if `greeting` is accessed more than once every
30 seconds the TTL increases by one minute up to ten minutes. Infrequent
accesses reduce the TTL but never below one minute.

## Redis Cache

`RedisCache` uses [go-redis](https://github.com/redis/go-redis) and maps cache operations to Redis commands:

```go
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
c := cache.NewRedis[string](client, nil) // uses JSON serialization by default
```

Both caches implement the same interface and can be swapped depending on deployment needs.

## Metrics

`LRUCache` can emit Prometheus metrics for cache hits, misses, evictions and operation latency. Metrics are registered on a
registry created with `metrics.NewRegistry` and exposed via the standard Prometheus HTTP handler:

```go
 reg := metrics.NewRegistry()
 c := cache.NewLRU[string](cache.WithMetrics[string](reg))
http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
```

Prometheus can then scrape the endpoint using a configuration similar to:

```yaml
scrape_configs:
  - job_name: "warp"
    static_configs:
      - targets: ["localhost:2112"]
```
