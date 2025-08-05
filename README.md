# Warp

Warp is a data orchestration and synchronization layer for distributed Go backends. It sits between your application and the primary storage, providing fast access to hot data with declarative consistency modes, pluggable caching and optional distributed invalidation.

## Features

- **Configurable Consistency** – choose between strong local, eventual distributed and strong distributed modes per key.
- **Pluggable Cache** – in-memory and Redis caches with TTL, warmup and metrics.
- **Storage Adapters** – abstract fallback storage for warmup and persistent writes.
- **Sync Bus** – propagate invalidations across nodes through a pub/sub interface.
- **Merge Engine** – resolve conflicts with last-write-wins or custom merge functions.
- **Validator** – background process to detect and optionally heal cache/store mismatches.

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
    store := adapter.NewInMemoryStore()
    w := core.New(cache.NewInMemory(), store, nil, merge.NewEngine())
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
    store := adapter.NewInMemoryStore()
    bus := syncbus.NewInMemoryBus()
    engine := merge.NewEngine()
    engine.Register("counter", func(old, new any) (any, error) {
        return old.(int) + new.(int), nil
    })

    w1 := core.New(cache.NewInMemory(), store, bus, engine)
    w2 := core.New(cache.NewInMemory(), store, bus, engine)

    w1.Register("counter", core.ModeEventualDistributed, time.Minute)
    w2.Register("counter", core.ModeEventualDistributed, time.Minute)

    ch, _ := bus.Subscribe(ctx, "counter")
    defer bus.Unsubscribe(ctx, "counter", ch)
    go func() {
        for range ch {
            w2.Invalidate(ctx, "counter")
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

See the [docs](docs/overview.md) directory for detailed guides on each module.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
