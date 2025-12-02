# Merge Engine

The `merge` package handles conflict resolution for concurrent updates. It allows you to define custom logic for merging values instead of the default "Last-Write-Wins" strategy.

## Overview

In a distributed system, or even concurrently on a single node, multiple writers might update the same key.
- **Default Behavior**: The last write overwrites the previous value.
- **Merge Engine**: Allows you to inspect the `old` value and the `new` value and produce a `merged` result.

## API Reference

### `NewEngine`

Creates a new Merge Engine.

```go
engine := merge.NewEngine[int]()
```

### `Register`

Registers a merge function for a specific key pattern.

```go
// func Register(pattern string, fn MergeFn[T])
engine.Register("counter", func(old, new int) (int, error) {
    return old + new, nil
})
```

### `MergeFn`

The signature of a merge function.

```go
type MergeFn[T any] func(old, new T) (T, error)
```

### `Value`

Wraps a value with a timestamp used by merge strategies.

```go
type Value[T any] struct {
    Data      T
    Timestamp time.Time
}
```

### `Strategy` Interface

Defines how two values should be merged.

```go
type Strategy[T any] interface {
    Merge(old, new Value[T]) Value[T]
}
```

### `Engine` Methods

```go
func (e *Engine[T]) Merge(key string, old, new Value[T]) (Value[T], error)
```
Merges two values based on the registered function or the default strategy.

## Examples

### 1. Counter (Sum)

Useful for distributed counters where you want to capture all increments.

```go
w.Merge("stats:visits", func(old, new int) (int, error) {
    return old + new, nil
})

// Thread 1: Set(1)
// Thread 2: Set(1)
// Result: 2 (instead of 1)
```

### 2. Append to List

Useful for logging or activity streams.

```go
w.Merge("user:logs", func(old, new []string) ([]string, error) {
    // Append new logs to old logs
    return append(old, new...), nil
})
```

### 3. CRDT-like Maps

Merge fields of a struct/map.

```go
type UserProfile struct {
    Name  string
    Email string
    Age   int
}

w.Merge("user:profile", func(old, new UserProfile) (UserProfile, error) {
    merged := old
    if new.Name != "" { merged.Name = new.Name }
    if new.Email != "" { merged.Email = new.Email }
    if new.Age != 0 { merged.Age = new.Age }
    return merged, nil
})
```

## How It Works with Core

When you call `w.Set(key, value)`:
1. Warp fetches the current value from Cache (or Store).
2. If a value exists, it checks if a Merge Function is registered for `key`.
3. If yes, it calls `fn(current, value)`.
4. The result is then saved to Cache and Store.

> [!NOTE]
> This happens **before** the value is sent to the Sync Bus (in distributed modes). This means the "merge" is local to the node performing the write. For strong consistency with merging, use `ModeStrongDistributed` which ensures serialized access via quorum.
