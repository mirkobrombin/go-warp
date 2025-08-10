# Getting Started

This guide walks through installing Warp, running a minimal example, and progressively enabling advanced features such as distributed invalidation, leases, the merge engine, and metrics.

## Installation

Install the library using `go get`:

```bash
go get github.com/mirkobrombin/go-warp/v1
```

## Minimal Example

The following program sets and retrieves a value using the in-memory cache:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/mirkobrombin/go-warp/v1/adapter"
    "github.com/mirkobrombin/go-warp/v1/cache"
    "github.com/mirkobrombin/go-warp/v1/core"
    "github.com/mirkobrombin/go-warp/v1/merge"
)

func main() {
    ctx := context.Background()
    store := adapter.NewInMemoryStore[string]()
    w := core.New[string](cache.NewInMemory[merge.Value[string]](), store, nil, merge.NewEngine[string]())
    w.Register("greeting", core.ModeStrongLocal, time.Minute)
    _ = w.Set(ctx, "greeting", "hello")
    v, _ := w.Get(ctx, "greeting")
    fmt.Println(v)
}
```

## Progressing to Advanced Features

### Distributed Invalidation

To propagate invalidations across nodes, configure a bus implementation such as the in-memory bus:

```go
bus := syncbus.NewInMemoryBus()
w := core.New[string](cache.NewInMemory[merge.Value[string]](), store, bus, merge.NewEngine[string]())
```

See the [Sync Bus](syncbus.md) documentation and glossary entries for [NATS](glossary.md#nats) and [Kafka](glossary.md#kafka) for production deployments.

### Leases

Leases group keys under a revocable token. When the lease is revoked, all attached keys are invalidated across the cluster:

```go
id, _ := w.GrantLease(ctx, time.Minute)
w.AttachKey(id, "example")
w.RevokeLease(ctx, id)
```

Learn more in [Leases](leases.md) and the [glossary](glossary.md#leases).

### Merge Engine

Custom merge functions resolve conflicting writes:

```go
engine := merge.NewEngine[int]()
engine.Register("counter", func(old, new int) (int, error) {
    return old + new, nil
})
w := core.New[int](cache.NewInMemory[merge.Value[int]](), store, bus, engine)
```

See [Merge Engine](merge.md) for details.

### Metrics

Warp components expose Prometheus metrics:

```go
reg := metrics.NewRegistry()
c := cache.NewLRU[string](cache.WithMetrics[string](reg))
http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
```

Refer to [Metrics](metrics.md) for available counters and gauges.

