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

The available modes are summarized below:

| Mode | Behavior | Sync Bus | Store | Default |
| ---- | -------- | -------- | ----- | ------- |
| `ModeStrongLocal` | Data is kept locally with optional fallback to the store. | Not used | Optional | No |
| `ModeEventualDistributed` | Invalidations are propagated asynchronously via the sync bus. | Required | Required | No |
| `ModeStrongDistributed` | Reserved for quorum based writes. | Required | Required | No |

There is no default mode; each key must explicitly choose one when registered.

### Strong Distributed Quorum

Strong distributed registrations coordinate writes through the sync bus.
`Set`, `Invalidate` and transactional commits block until the configured
quorum acknowledges the invalidation event. The default quorum is `1` and can
be increased with `SetQuorum`:

```go
w.Register("orders", core.ModeStrongDistributed, time.Minute)
w.SetQuorum("orders", 3) // wait for three replicas
```

Warp requires a bus implementation that supports quorum acknowledgements via
`PublishAndAwait`. If the configured bus does not expose quorum semantics it
must return `syncbus.ErrQuorumUnsupported`. When the required number of
replicas is not reached the call fails with `syncbus.ErrQuorumNotSatisfied`.
Using strong distributed mode without a bus returns `core.ErrBusRequired`.

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

## Transactions

Transactions batch multiple operations and apply them atomically. They can
mix `Set`, `Delete` and CAS checks while still benefiting from custom merge
functions:

```go
ctx := context.Background()
w.Merge("counter", func(old, new int) (int, error) {
    return old + new, nil
})
w.Merge("logs", func(old, new []string) ([]string, error) {
    return append(old, new...), nil
})
txn := w.Txn(ctx)
txn.Set("counter", 1)
txn.Set("logs", []string{"start"})
txn.Delete("obsolete")
txn.Delete("temp")
txn.CompareAndSwap("status", "draft", "live")
if err := txn.Commit(); err != nil {
    // handle error
}
```

## Warmup and Validation

`Warmup` preloads registered keys from the storage adapter during boot. The `Validator` allows running background consistency checks:

```go
w.Warmup(ctx)
validator := w.Validator(validator.ModeAlert, time.Minute)
go validator.Run(ctx)
```

See the [overview](overview.md) for the list of modules and the individual documents for more details.

## Metrics

`core` can expose Prometheus metrics for cache hits, misses, evictions and operation latency. Create a registry with
`metrics.NewRegistry` and enable metrics on both the cache and core:

```go
reg := metrics.NewRegistry()
c := cache.NewInMemory[merge.Value[string]](cache.WithMetrics[merge.Value[string]](reg))
w := core.New[string](c, adapter.NewInMemoryStore[string](), nil, merge.NewEngine[string](), core.WithMetrics[string](reg))
http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
```

Prometheus scraping example:

```yaml
scrape_configs:
  - job_name: "warp"
    static_configs:
      - targets: ["localhost:2112"]
```
