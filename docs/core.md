# Warp Core

The `core` package provides the primary API used by applications. It coordinates the cache, storage adapter, sync bus and merge engine to provide fast and controlled access to data.

## Registration

Each key must be registered with a consistency mode and TTL:

```go
w := core.New[string](cache.NewInMemory[merge.Value[string]](), adapter.NewInMemoryStore[string](), nil, merge.NewEngine[string]())
w.Register("greeting", core.ModeStrongLocal, time.Minute)
```

To remove a registration, call `Unregister`:

```go
w.Unregister("greeting")
```

The available modes are:

- `ModeStrongLocal` – data is kept locally with optional fallback to the store.
- `ModeEventualDistributed` – invalidations are propagated asynchronously via the sync bus.
- `ModeStrongDistributed` – reserved for quorum based writes.

## Basic Operations

```go
ctx := context.Background()
if err := w.Set(ctx, "greeting", "hello"); err != nil {
    // handle error
}
value, err := w.Get(ctx, "greeting")
w.Invalidate(ctx, "greeting")
```

`Get` falls back to the storage adapter on miss. `Set` stores the value, applies merge strategies and publishes invalidations when required.

## Custom Merge Functions

Custom merge functions can be registered per key through the merge engine:

```go
w.Merge("counter", func(old, new int) (int, error) {
    return old + new, nil
})
```

## Warmup and Validation

`Warmup` preloads registered keys from the storage adapter during boot. The `Validator` allows running background consistency checks:

```go
w.Warmup(ctx)
validator := w.Validator(validator.ModeAlert, time.Minute)
go validator.Run(ctx)
```

See the [overview](overview.md) for the list of modules and the individual documents for more details.
