<div align="center">
  <h1>go-warp v2</h1>
  <p>Data coordination and cache synchronization for distributed Go services.</p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go 1.25+">
    <img src="https://img.shields.io/badge/Foundation-v2.4.0-success" alt="Foundation v2.4.0">
    <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT">
  </p>
</div>

Warp keeps hot data close to an application while coordinating invalidation,
merge, locking, leases, and consistency across nodes. It sits between an L1
cache, a primary store, and a transport such as Redis, NATS, Kafka, or Warp
Mesh.

It is a library, not a cache server. The caller chooses storage, transport,
consistency mode, TTL policy, and failure behavior. The sidecar is available
when a non-Go process needs the same data path over RESP.

## Install

```sh
go get github.com/mirkobrombin/go-warp/v2@latest
```

Version 2 requires Go 1.25 and
`github.com/mirkobrombin/go-foundation/v2`. The module and every public
package now use the `/v2` import path. See the
[v2 migration guide](docs/v2-migration.md) before upgrading an existing
application.

## Quick start

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/mirkobrombin/go-warp/v2/core"
    "github.com/mirkobrombin/go-warp/v2/presets"
)

type User struct {
    Name string
}

func main() {
    ctx := context.Background()
    w := presets.NewInMemoryStandalone[User]()
    w.Register("user:123", core.ModeStrongLocal, 10*time.Minute)

    if err := w.Set(ctx, "user:123", User{Name: "Alice"}); err != nil {
        panic(err)
    }
    user, err := w.Get(ctx, "user:123")
    if err != nil {
        panic(err)
    }
    fmt.Println(user.Name)
}
```

Registrations use exact keys. Register each key before `Get`, `Set`, or
`Invalidate`; `GetOrSet` can register a missing key with local defaults.

## Consistency modes

| Mode | Write behavior | Typical use |
| --- | --- | --- |
| `ModeStrongLocal` | Updates the local cache and store without network coordination. | Local state and single-node services |
| `ModeEventualDistributed` | Applies locally, then publishes invalidation in the background. | Shared cached data |
| `ModeStrongDistributed` | Waits for the configured bus acknowledgement before applying locally. | Coordinated writes on supported buses |

NATS Core does not provide topology-aware quorum. The
`NewNATSStrong` preset records intent but does not turn NATS Core into a
quorum transport.

## Packages

| Package | Purpose |
| --- | --- |
| `adapter` | Primary storage contracts and Redis/GORM adapters |
| `cache` | In-memory, Redis, Ristretto, adaptive, and versioned caches |
| `core` | Registration, reads, writes, invalidation, warmup, and transactions |
| `lock` | In-memory, Redis, and NATS distributed locks |
| `merge` | Last-write-wins, vector clocks, and custom merge functions |
| `metrics` | Prometheus metrics |
| `presets` | Common Redis, NATS, mesh, and standalone assemblies |
| `syncbus` | In-memory, Redis, NATS, Kafka, and mesh invalidation buses |
| `streambus` | Bounded high-volume streams, replay, QoS, and WebTransport |
| `validator` | Background cache/store consistency checks |
| `watchbus` | In-memory, Redis, NATS, and HTTP watch streams |

Runnable programs live under [examples/v2](examples/v2).

## Sidecar

`warp-proxy` exposes Warp through the Redis Serialization Protocol for
processes that cannot import the Go module:

```sh
go install github.com/mirkobrombin/go-warp/v2/cmd/warp-proxy@latest
```

The command implements the supported RESP subset documented in
[Sidecar mode](docs/sidecar.md).

## Benchmarks

Run cache benchmarks on the current toolchain:

```sh
go test -run '^$' -bench . -benchmem ./cache
```

Recorded results and the standalone load tool are documented in
[Cache benchmarks](docs/benchmark.md).

## Documentation

- [Overview](docs/overview.md)
- [Getting started](docs/getting-started.md)
- [Core and consistency](docs/core.md)
- [Caches](docs/cache.md)
- [Storage adapters](docs/adapter.md)
- [Synchronization buses](docs/syncbus.md)
- [StreamBus and WebTransport](docs/streambus.md)
- [Distributed locks](docs/lock.md)
- [Leases](docs/leases.md)
- [Presets](docs/presets.md)
- [Mesh](docs/mesh.md)
- [Sidecar](docs/sidecar.md)
- [v2 migration](docs/v2-migration.md)

## License

MIT
