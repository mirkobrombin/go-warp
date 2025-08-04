# Storage Adapter

The `adapter` package abstracts access to the primary storage used as fallback and warmup source.

## Store Interface

```go
type Store interface {
    Get(ctx context.Context, key string) (any, error)
    Set(ctx context.Context, key string, value any) error
    Keys(ctx context.Context) ([]string, error)
}
```

## In-Memory Store

`InMemoryStore` is a simple reference implementation backed by a map. It is useful for tests and examples:

```go
store := adapter.NewInMemoryStore()
_ = store.Set(ctx, "foo", "bar")
value, _ := store.Get(ctx, "foo")
```

Custom adapters can be implemented to connect Warp with databases or other storage systems.
