# Distributed Lock

Warp provides a distributed locking primitive built on top of the Sync Bus.

## API Reference

### `NewInMemory`

```go
func NewInMemory(bus syncbus.Bus) *InMemory
```
Creates a new in-memory locker that uses the provided bus for coordination.

### `NewRedis`

```go
func NewRedis(client *redis.Client, bus syncbus.Bus) *Redis
```
Creates a new Redis-backed locker.

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
