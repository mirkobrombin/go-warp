# Presets

The `presets` package offers pre-configured factory functions to simplify Warp initialization for common use cases. Instead of manually wiring the Cache, Store, Bus, and Engine, you can use these one-liners to get a production-ready setup.

## v1/presets

```go
import "github.com/mirkobrombin/go-warp/v1/presets"
```

### `NewRedisEventual`

Creates a Warp instance tailored for **high-performance caching** where eventual consistency is acceptable.

- **Architecture**:
    - **L1**: In-Memory Cache (LRU).
    - **L2**: Redis Store.
    - **Bus**: Redis Pub/Sub (for eventual invalidation).
    - **Consistency**: Optimized for `ModeEventualDistributed` (writes go to L2, asynchronous invalidation to L1 peers).

```go
w := presets.NewRedisEventual[User](presets.RedisOptions{
    Addr: "localhost:6379",
})
```

### `NewRedisStrong`

Creates a Warp instance tailored for **strong consistency**.

- **Architecture**:
    - **L1**: In-Memory Cache.
    - **L2**: Redis Store.
    - **Bus**: Redis Pub/Sub.
    - **Consistency**: Optimized for `ModeStrongDistributed` (writes wait for quorum/acknowledgment before returning).

```go
w := presets.NewRedisStrong[Config](presets.RedisOptions{
    Addr: "localhost:6379",
})

// Remember to register keys with ModeStrongDistributed
w.Register("config:*", core.ModeStrongDistributed, 24*time.Hour)
w.SetQuorum("config:*", 3)
```

> [!NOTE]
> Strong consistency requires all nodes to be reachable to satisfy quorum.

### `NewInMemoryStandalone`

Creates a fully isolated Warp instance with **no external dependencies**. Useful for local development, testing, or simplistic single-node caching.

- **Architecture**:
    - **L1**: In-Memory Cache.
    - **L2**: In-Memory Store (map).
    - **Bus**: In-Memory Bus (messages simulate network delay locally).

```go
w := presets.NewInMemoryStandalone[Session]()
```
