# Storage Adapter

The `adapter` package abstracts access to the primary storage used as fallback and warmup source.

## Store Interface

```go
type Store[T any] interface {
    Get(ctx context.Context, key string) (T, bool, error)
    Set(ctx context.Context, key string, value T) error
    Keys(ctx context.Context) ([]string, error)
}
```

## In-Memory Store

`InMemoryStore` is a simple reference implementation backed by a map. It is useful for tests and examples:

```go
store := adapter.NewInMemoryStore[string]()
_ = store.Set(ctx, "foo", "bar")
value, ok, _ := store.Get(ctx, "foo")
```

Custom adapters can be implemented to connect Warp with databases or other storage systems.

## Redis Store

`RedisStore` persists values using a Redis instance. It uses the Go-Redis client
for connection pooling and context-aware operations:

```go
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
store := adapter.NewRedisStore[string](client, adapter.WithTimeout(5*time.Second))
```

#### Options

- **`WithTimeout(d time.Duration)`**: Sets the operation timeout for Redis calls.

## Batch Operations

Stores that support batching implement the `Batcher` interface.

```go
type Batcher[T any] interface {
    Batch(ctx context.Context) (Batch[T], error)
}

type Batch[T any] interface {
    Set(ctx context.Context, key string, value T) error
    Delete(ctx context.Context, key string) error
    Commit(ctx context.Context) error
}
```

The store can then be passed to `core.New` so that Warp reads and writes
through Redis, enabling persistence and warmup.
