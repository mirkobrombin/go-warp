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

Results from `cmd/bench/` suite running on a standard developer machine (Docker/Podman):

| System | Mode | Ops/sec | Avg Latency | P99 Latency |
| :--- | :--- | :--- | :--- | :--- |
| **Warp** | **StrongLocal** | **~2,700,000** | **370 ns** | **0.6 ms** |
| **Warp** | **Eventual (Mesh)** | **~3,000,000** | **330 ns** | **0.6 ms** |
| **Warp** | **Eventual (L2=Redis)** | **~2,400,000** | **400 ns** | **0.8 ms** |
| Ristretto | Local Cache | ~12,000,000 | 80 ns | 0.004 ms |
| Redis | Remote | ~55,000 | 18 µs | 1.6 ms |
| DragonFly | Remote | ~118,000 | 8.5 µs | 1.1 ms |
| **Warp Proxy** | **Sidecar (Node.js)** | **~170,000** | **-** | **Pipelined (Batch=50)** |
| **Warp Proxy** | **Sidecar (Python)** | **~80,000** | **-** | **Pipelined (Batch=50)** |

> **Takeaway**: Warp allows you to access data **~10-20x faster than standard Redis** by serving reads from memory (L1) while maintaining eventual consistency across the cluster.

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
  - [Sync Bus](docs/syncbus.md): NATS, Kafka, Warp Mesh (UDP/Gossip), Custom Adapters.
  - [Lock](docs/lock.md): Distributed Locking.
  - [Leases](docs/leases.md): Group Invalidation.
  - [Merge](docs/merge.md): Conflict Resolution.
  - [Validator](docs/validator.md): Background Consistency Checks.
  - [Federation](docs/federation.md): Multi-region synchronization.
  - [Sidecar](docs/sidecar.md): Polyglot proxy (RESP protocol).
- **[Glossary](docs/glossary.md)**: Terminology definition.

## Client Libraries

- **[Warp Store (JS)](warp-store/README.md)**: A high-performance, dual-layer (Memory + IndexedDB) caching store for frontend applications. Supports offline mode, session restoration, and is designed to work seamlessly with Warp's backend concepts.
- **[Warp Sidecar](docs/sidecar.md)**: A polyglot proxy that allows you to access Warp's unified state from any language.

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

### Warp Mesh (Zero-Infra)

Warp Mesh allows you to cluster your Go backends without Redis, NATS, or Kafka. It uses UDP Multicast for local discovery and a Unicast Gossip protocol for cloud environments.

```go
w := presets.NewMeshEventual[User](mesh.MeshOptions{
    Port: 7946,
    Peers: []string{"10.0.0.1:7946"}, // Static seeds for cloud
})
```

## License

MIT License. See [LICENSE](LICENSE) for details.

## IA

I am using Gemini, to help me write the documentation and debug issues, since the cocebase is really large. I want to make sure I am not missing anything important as this library aims to be used in critical production systems.

I am just honest and transparent. Peace and love.
