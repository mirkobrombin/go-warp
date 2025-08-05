# Sync Bus

The `syncbus` package provides a pluggable mechanism to propagate invalidations across nodes.

## Bus Interface

```go
type Bus interface {
    Publish(ctx context.Context, key string) error
    Subscribe(ctx context.Context, key string) (chan struct{}, error)
    Unsubscribe(ctx context.Context, key string, ch chan struct{}) error
}
```

Subscriptions are automatically cleaned up when the context passed to
`Subscribe` is done, but can also be explicitly removed via
`Unsubscribe`.

## In-Memory Bus

`InMemoryBus` is a local implementation mainly for development and tests. It deduplicates events and exposes basic metrics:

```go
bus := syncbus.NewInMemoryBus()
ch, _ := bus.Subscribe(ctx, "greeting")
defer bus.Unsubscribe(ctx, "greeting", ch)
go func() { for range ch { fmt.Println("invalidated") } }()
_ = bus.Publish(ctx, "greeting")
metrics := bus.Metrics() // Published, Delivered
```

Other adapters (e.g. Redis Streams, NATS, Kafka) can be built on top of the same interface.
