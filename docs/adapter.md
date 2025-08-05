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
