# Distributed Lock

Warp provides a distributed locking primitive with multiple backends.

## API Reference

### `NewInMemory`

```go
func NewInMemory(bus syncbus.Bus) *InMemory
```
Creates a new in-memory locker that uses the provided bus for coordination.

### `NewRedis`

```go
func NewRedis(client redis.UniversalClient) *Redis
```
Creates a new Redis-backed locker using `SET NX EX` for atomic acquisition and
a Lua ownership-check script for safe release. `client` may be a standalone,
Sentinel, or cluster client (`redis.UniversalClient`).

#### Acquire strategy
`Acquire` subscribes to `__keyevent@0__:del` and `__keyevent@0__:expired` for
prompt wake-up when the lock is freed. If keyspace notifications are disabled
on the server it falls back automatically to exponential-backoff polling
(5 ms → 500 ms). Enable notifications with `notify-keyspace-events "KEx"` for
best latency.

#### Ownership guarantee
Each `TryLock` generates a unique UUID token stored as the Redis key's value.
`Release` runs a Lua script that deletes the key only when the stored value
matches the caller's token — preventing any other holder from being evicted.

### `Locker` Interface

```go
type Locker interface {
    Acquire(ctx context.Context, key string, ttl time.Duration) error
    TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
    Release(ctx context.Context, key string) error
}
```

### `Acquire`

```go
func (l *Locker) Acquire(ctx context.Context, key string, ttl time.Duration) error
```
Attempts to acquire the lock. Blocks until acquired or context expires.
- **key**: Resource identifier.
- **ttl**: Safety timeout. If the holder crashes, the lock is auto-released after this duration.

### `TryLock`

```go
func (l *Locker) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
```
Non-blocking attempt. Returns `true` if acquired immediately, `false` otherwise.

### `Release`

```go
func (l *Locker) Release(ctx context.Context, key string) error
```
Releases the lock and notifies waiting nodes.

## Use Cases

### 1. Leader Election
Ensure only one node performs a scheduled task.

### 2. Resource Exclusive Access
Prevent race conditions when modifying a shared external resource.
