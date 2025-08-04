# Cache Layer

The `cache` package defines the `Cache` interface and provides in-memory and Redis implementations.

## Interface

```go
type Cache interface {
    Get(ctx context.Context, key string) (any, bool)
    Set(ctx context.Context, key string, value any, ttl time.Duration)
    Invalidate(ctx context.Context, key string)
}
```

## In-Memory Cache

`InMemoryCache` is a simple map based cache with TTL support and basic metrics:

```go
c := cache.NewInMemory()
stats := c.Metrics() // Hits, Misses, Size
```

## Redis Cache

`RedisCache` uses [go-redis](https://github.com/redis/go-redis) and maps cache operations to Redis commands:

```go
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
c := cache.NewRedis(client)
```

Both caches implement the same interface and can be swapped depending on deployment needs.
