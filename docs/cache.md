# Cache Layer

The `cache` package defines the `Cache` interface and provides in-memory and Redis implementations.

## Interface

```go
type Cache[T any] interface {
    Get(ctx context.Context, key string) (T, bool)
    Set(ctx context.Context, key string, value T, ttl time.Duration)
    Invalidate(ctx context.Context, key string)
}
```

## In-Memory Cache

`InMemoryCache` is a simple map based cache with TTL support and basic metrics.
It also runs a background goroutine that periodically removes expired items.
The sweep happens every minute by default and can be customized:

```go
c := cache.NewInMemory[string](cache.WithSweepInterval[string](30 * time.Second))
stats := c.Metrics() // Hits, Misses, Size
```

The sweeper adds a small amount of overhead as it iterates over cached items at
each interval. For most workloads this is negligible, but very large caches may
see increased CPU usage during sweeps.

You can also limit the number of items stored by providing `WithMaxEntries`. When
the cache grows beyond this limit the least recently used item is evicted:

```go
c := cache.NewInMemory[string](cache.WithMaxEntries[string](100))
```

Calls to `Get` mark items as recently used, ensuring that frequently accessed
entries remain in the cache.

## Redis Cache

`RedisCache` uses [go-redis](https://github.com/redis/go-redis) and maps cache operations to Redis commands:

```go
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
c := cache.NewRedis[string](client, nil) // uses JSON serialization by default
```

Both caches implement the same interface and can be swapped depending on deployment needs.
