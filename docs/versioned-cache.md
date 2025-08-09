# Versioned Cache

The `cache/versioned` package wraps a `cache.Cache` to keep a history of values
per key. Each update is stored with its timestamp allowing lookups at specific
points in time.

```go
base := cache.NewInMemory[merge.VersionedValue[int]]()
vc := versioned.New[int](base, 5)
```

The history length is limited by the configured `limit` parameter.
