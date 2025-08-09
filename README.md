# Warp
Warp is a data orchestration and synchronization layer for distributed Go backends. It sits between your application and the primary storage, providing fast access to hot data with declarative consistency modes, pluggable caching and optional distributed invalidation.

## Features

- **Configurable Consistency** – choose between strong local, eventual distributed and strong distributed modes per key.
- **Pluggable Cache** – in-memory and Redis caches with TTL, warmup, metrics and
  background eviction of expired items.
- **Storage Adapters** – abstract fallback storage for warmup and persistent writes.
- **Sync Bus** – propagate invalidations across nodes through a pub/sub interface.
- **Merge Engine** – resolve conflicts with last-write-wins or custom merge functions.
- **Validator** – background process to detect and optionally heal cache/store mismatches.
- [**Lock**](docs/lock.md) – distributed locking primitives built on the sync bus.
- [**Leases**](docs/leases.md) – group keys under revocable leases.
- [**Versioned Cache**](docs/versioned-cache.md) – keep a history of values per key.
- [**Watch Bus**](docs/watchbus.md) – lightweight message bus for streaming byte payloads.
- [**Metrics**](docs/metrics.md) – Prometheus counters and gauges for Warp components.

## Installation

```bash
go get github.com/mirkobrombin/go-warp/v1
```

## Basic Usage

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
    w.Warmup(ctx) // optional warmup from store
    if err := w.Set(ctx, "greeting", "hello"); err != nil {
        panic(err)
    }
    v, _ := w.Get(ctx, "greeting")
    fmt.Println(v)
}
```

## Advanced Example

The following example demonstrates custom merge logic and distributed invalidation between two nodes using the in-memory bus:

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
    "github.com/mirkobrombin/go-warp/v1/syncbus"
)

func main() {
    ctx := context.Background()
    store := adapter.NewInMemoryStore[int]()
    bus := syncbus.NewInMemoryBus()
    engine := merge.NewEngine[int]()
    engine.Register("counter", func(old, new int) (int, error) {
        return old + new, nil
    })

    w1 := core.New[int](cache.NewInMemory[merge.Value[int]](), store, bus, engine)
    w2 := core.New[int](cache.NewInMemory[merge.Value[int]](), store, bus, engine)

    w1.Register("counter", core.ModeEventualDistributed, time.Minute)
    w2.Register("counter", core.ModeEventualDistributed, time.Minute)

    ch, _ := bus.Subscribe(ctx, "counter")
    defer bus.Unsubscribe(ctx, "counter", ch)
    go func() {
        for range ch {
            _ = w2.Invalidate(ctx, "counter")
        }
    }()

    if err := w1.Set(ctx, "counter", 10); err != nil {
        panic(err)
    }
    if err := w2.Set(ctx, "counter", 5); err != nil {
        panic(err)
    }

    time.Sleep(10 * time.Millisecond)
    v1, _ := w1.Get(ctx, "counter")
    v2, _ := w2.Get(ctx, "counter")
    fmt.Println("node1:", v1, "node2:", v2)
}
```

## Documentation

See the [docs](docs/overview.md) directory for detailed guides on each module, including [deployment architecture](docs/overview.md#deployment).

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
