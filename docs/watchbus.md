# Watch Bus

The `watchbus` package implements a lightweight message bus for streaming byte
payloads. Clients can watch a specific key or subscribe to a key prefix.

```go
bus := watchbus.NewInMemory()
ch, _ := bus.Watch(ctx, "foo")
_ = bus.Publish(ctx, "foo", []byte("data"))
msg := <-ch
```

Prefix subscriptions receive updates for all matching keys via `PublishPrefix`.
