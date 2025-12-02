# Getting Started

This guide walks through installing Warp and building a realistic application: a **User Session Store**. We will start simple and progressively enable advanced features like distributed invalidation, leases, and metrics.

## Installation

Install the library using `go get`:

```bash
go get github.com/mirkobrombin/go-warp/v1
```

## Scenario: User Session Store

Imagine we are building a backend for a web application. We need to store user sessions and we need it to have:
- Fast access (cache)
- Persistence (database fallback)
- When a user logs out, their session must be invalidated immediately across all nodes

### Step 1: Basic Setup (Local Cache + Store)

First, we set up Warp with an in-memory cache and a simulated persistent store.

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

type Session struct {
    UserID    string
    Username  string
    ExpiresAt time.Time
}

func main() {
    ctx := context.Background()

    // 1. Storage Adapter: In a real app, this would be the Redis adapter or a custom adapter for your database (e.g. Postgres, MySQL).
    // We use an in-memory store for this example.
    store := adapter.NewInMemoryStore[Session]()

    // 2. Cache: We use an in-memory LRU cache.
    c := cache.NewInMemory[merge.Value[Session]]()

    // 3. Warp Core: The orchestrator.
    w := core.New[Session](c, store, nil, merge.NewEngine[Session]())

    // 4. Registration: We MUST register keys before using them.
    // ModeStrongLocal: Strong consistency on this node, no distributed events.
    w.Register("session:*", core.ModeStrongLocal, 30*time.Minute)

    // Usage
    sess := Session{UserID: "u123", Username: "alice"}
    
    // Set: Writes to Cache AND Store
    if err := w.Set(ctx, "session:u123", sess); err != nil {
        panic(err)
    }

    // Get: Reads from Cache (fast). If missing, fetches from Store and populates Cache.
    v, err := w.Get(ctx, "session:u123")
    if err != nil {
        panic(err)
    }
    fmt.Printf("Session found: %+v\n", v)
}
```

### Step 2: Distributed Invalidation

Now, imagine we scale our application to multiple nodes. If Alice logs out on Node A, Node B must also invalidate her session. We use `ModeEventualDistributed` and a `SyncBus`.

```go
import "github.com/mirkobrombin/go-warp/v1/syncbus"

// ... inside main ...

// 1. Sync Bus: Connects nodes. In production, use NATS or Kafka.
// Here we use InMemoryBus to simulate it locally.
bus := syncbus.NewInMemoryBus()

w := core.New[Session](c, store, bus, merge.NewEngine[Session]())

// 2. Change Mode: EventualDistributed ensures invalidations are propagated.
w.Register("session:*", core.ModeEventualDistributed, 30*time.Minute)

// Node A invalidates
w.Invalidate(ctx, "session:u123")

// Node B (listening on the same bus) will automatically remove "session:u123" from its cache.
```

> [!TIP]
> Use `ModeEventualDistributed` for most cache scenarios where "eventual" consistency is acceptable. The invalidation happens milliseconds after the write.

### Step 3: Leases (Group Invalidation)

What if we want to invalidate *all* sessions for a specific tenant or organization? Or maybe a "kill switch" for a user's devices? Leases allow grouping keys.

```go
// Create a lease for User u123
leaseID, _ := w.GrantLease(ctx, 24*time.Hour)

// Attach specific sessions to this lease
w.AttachKey(leaseID, "session:u123:mobile")
w.AttachKey(leaseID, "session:u123:desktop")

// Revoke the lease: Invalidates BOTH "session:u123:mobile" and "session:u123:desktop"
w.RevokeLease(ctx, leaseID)
```

### Step 4: Metrics

Observability is critical. Warp exposes Prometheus metrics out of the box.

```go
import (
    "net/http"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/mirkobrombin/go-warp/v1/metrics"
)

// ...

reg := prometheus.NewRegistry()

// Enable metrics on Cache and Core
c := cache.NewInMemory[merge.Value[Session]](cache.WithMetrics[merge.Value[Session]](reg))
w := core.New[Session](c, store, bus, nil, core.WithMetrics[Session](reg))

// Expose endpoint
http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
go http.ListenAndServe(":2112", nil)
```

## Next Steps

- **[Core Concepts](core.md)**: Deep dive into Consistency Modes and Architecture.
- **[Best Practices](best-practices.md)**: Recommended patterns for different use cases.
- **[Sync Bus](syncbus.md)**: How to configure NATS/Kafka/Redis adapters.
