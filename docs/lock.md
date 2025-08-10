# Lock

The `lock` package offers distributed locking primitives built on top of the `syncbus`.
The in-memory implementation coordinates locks across nodes by publishing lock and
unlock events on the bus.

## In-Memory Lock

```go
l := lock.NewInMemory(syncbus.NewInMemoryBus())
ok, _ := l.TryLock(ctx, "key", time.Second)
if ok {
    defer l.Release(ctx, "key")
}
```

## NATS Lock

```go
nc, _ := nats.Connect(nats.DefaultURL)
l := lock.NewInMemory(syncbus.NewNATSBus(nc))
ok, _ := l.TryLock(ctx, "key", time.Second)
if ok {
    defer l.Release(ctx, "key")
}
```

Locks can expire automatically using the TTL parameter.
