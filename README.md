# Warp

**Warp** is a data orchestration and synchronization layer for distributed Go backends.

It sits between your application and your primary storage (Database, API), providing:
- **L1 Caching** (In-Memory)
- **Distributed Invalidation** (via NATS, Kafka, etc.)
- **Consistency Modes** (Strong Local, Eventual Distributed, Strong Distributed)
- **Distributed Locking & Leases**
- **Conflict Resolution** (Merge Engine)

## Why Warp?

Most caching libraries are just... caches. You `Set` a key, and it sits there until it expires. In a distributed system, this leads to stale data.

**Warp is different.** It treats data as a living entity that needs to be synchronized across your cluster.

- **Need speed?** Use `ModeStrongLocal`.
- **Need consistency?** Use `ModeEventualDistributed` and Warp will automatically notify other nodes when you update data.
- **Need coordination?** Use `ModeStrongDistributed` to ensure writes are acknowledged by a quorum of nodes.
- **Need to group data?** Use **Leases** to invalidate a user's entire session cache with one call.

## Documentation

- **[Getting Started](docs/getting-started.md)**: Build a User Session Store in 5 minutes.
- **[Core Concepts](docs/core.md)**: Understand Consistency Modes and Architecture.
- **[Best Practices](docs/best-practices.md)**: Recommended patterns for common scenarios.
- **[Modules](docs/overview.md)**:
  - [Cache](docs/cache.md): Adaptive TTL, Pluggable Backends.
  - [Sync Bus](docs/syncbus.md): NATS, Kafka, Custom Adapters.
  - [Lock](docs/lock.md): Distributed Locking.
  - [Leases](docs/leases.md): Group Invalidation.
  - [Merge](docs/merge.md): Conflict Resolution.
  - [Validator](docs/validator.md): Background Consistency Checks.
- **[Glossary](docs/glossary.md)**: Terminology definition.

## Installation

```bash
go get github.com/mirkobrombin/go-warp/v1
```

## Quick Example

```go
package main

import (
    "context"
    "time"
    "github.com/mirkobrombin/go-warp/v1/adapter"
    "github.com/mirkobrombin/go-warp/v1/cache"
    "github.com/mirkobrombin/go-warp/v1/core"
    "github.com/mirkobrombin/go-warp/v1/merge"
)

func main() {
    // 1. Setup
    store := adapter.NewInMemoryStore[string]()
    c := cache.NewInMemory[merge.Value[string]]()
    w := core.New[string](c, store, nil, merge.NewEngine[string]())

    // 2. Register Key
    w.Register("greeting", core.ModeStrongLocal, time.Minute)

    // 3. Use
    ctx := context.Background()
    w.Set(ctx, "greeting", "Hello Warp!")
}
```

## License

MIT License. See [LICENSE](LICENSE) for details.
