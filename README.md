# Warp

**Warp** is a data orchestration and synchronization layer for distributed Go backends.

It sits between your application and your primary storage (Database, API), providing:
- **L1 Caching** (In-Memory)
- **Distributed Invalidation** (via NATS, Kafka, etc.)
- **Consistency Modes** (Strong Local, Eventual Distributed, Strong Distributed)
- **Distributed Locking & Leases**
- **Conflict Resolution** (Merge Engine)

## Features

- **Request Coalescing**: Built-in `singleflight` protection against Thundering Herd.
- **Adaptive Batching**: Automatically groups and compresses invalidation events under high load.
- **Zero-Copy Serialization**: Optional `ByteCodec` for maximum throughput.
- **Polyglot Sidecar**: Connect standard Redis clients (Node.js, Python, etc.) via `warp-proxy`.
  > *Note: The Proxy is optimized for **Consistency and Interoperability**, providing access to Warp's unified state from any language. For raw millisecond-level throughput in Go, use the Core library directly.*

## Why Warp?

Most caching libraries are just... caches. You `Set` a key, and it sits there until it expires. In a distributed system, this leads to stale data.

**Warp is different.** It treats data as a living entity that needs to be synchronized across your cluster.

### Benchmarks

Results from `bench/` suite running on a standard developer machine (Docker/Podman):

| System | Mode | Ops/sec | Avg Latency | Notes |
| :--- | :--- | :--- | :--- | :--- |
| **Warp** | **Eventual (L2=Redis)** | **~2,500,000** | **390 ns** | **Hits L1 memory, syncs via Bus.** |
| **Warp** | StrongLocal | ~2,280,000 | 438 ns | Pure in-memory (No network). |
| Ristretto | Local Cache | ~11,800,000 | 85 ns | Raw cache speed (no Warp features). |
| Redis | Remote | ~108,000 | 9.2 µs | Network roundtrip required. |
| DragonFly | Remote | ~83,000* | 12.0 µs | *Client-side bottleneck.* |
| DragonFly | Remote | ~83,000* | 12.0 µs | *Client-side bottleneck.* |
| **Warp Proxy** | **Sidecar (Node.js)** | **~250,000** | **-** | **Pipelined (Batch=50).** |
| **Warp Proxy** | **Sidecar (Python)** | **~95,000** | **-** | **Pipelined (Batch=50).** |

> **Takeaway**: Warp allows you to access data **20x faster than standard Redis** by serving reads from memory (L1) while maintaining eventual consistency across the cluster.

- **Need speed?** Use `ModeStrongLocal`.
- **Need consistency?** Use `ModeEventualDistributed` and Warp will automatically notify other nodes when you update data.
- **Need coordination?** Use `ModeStrongDistributed` to ensure writes are acknowledged by a quorum of nodes.
- **Need to group data?** Use **Leases** to invalidate a user's entire session cache with one call.

## Documentation

- **[Getting Started](docs/getting-started.md)**: Build a User Session Store in 5 minutes.
- **[Presets](docs/presets.md)**: Production-ready configurations (`NewRedisEventual`, etc.).
- **[Core Concepts](docs/core.md)**: Understand Consistency Modes and Architecture.
- **[Best Practices](docs/best-practices.md)**: Recommended patterns for common scenarios.
- **[Modules](docs/overview.md)**:
  - [Cache](docs/cache.md): Adaptive TTL, Pluggable Backends.
  - [Sync Bus](docs/syncbus.md): NATS, Kafka, Custom Adapters.
  - [Lock](docs/lock.md): Distributed Locking.
  - [Leases](docs/leases.md): Group Invalidation.
  - [Merge](docs/merge.md): Conflict Resolution.
  - [Validator](docs/validator.md): Background Consistency Checks.
  - [Federation](docs/federation.md): Multi-region synchronization.
  - [Sidecar](docs/sidecar.md): Polyglot proxy (RESP protocol).
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
    "fmt"
    "time"
    "github.com/mirkobrombin/go-warp/v1/core"
    "github.com/mirkobrombin/go-warp/v1/presets"
)

type User struct {
    Name string
}

func main() {
    // 1. Setup with Preset (Production-ready)
    // Automatically wires In-Memory L1 + Redis L2 + Redis Bus
    w := presets.NewRedisEventual[User](presets.RedisOptions{
        Addr: "localhost:6379",
    })

    // 2. Register Key
    w.Register("user:*", core.ModeEventualDistributed, 10*time.Minute)

    // 3. Use
    ctx := context.Background()
    w.Set(ctx, "user:123", User{Name: "Alice"})
    
    val, _ := w.Get(ctx, "user:123")
    fmt.Println(val.Name)
}
```

## License

MIT License. See [LICENSE](LICENSE) for details.
