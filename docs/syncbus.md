# Sync Bus

The `syncbus` package provides a pluggable mechanism to propagate invalidations across nodes.

## Bus Interface

```go
type Bus interface {
    Publish(ctx context.Context, key string)
    Subscribe(ctx context.Context, key string) (<-chan struct{}, error)
}
```

## In-Memory Bus

`InMemoryBus` is a local implementation mainly for development and tests. It deduplicates events and exposes basic metrics:

```go
bus := syncbus.NewInMemoryBus()
ch, _ := bus.Subscribe(ctx, "greeting")
go func() { for range ch { fmt.Println("invalidated") } }()
bus.Publish(ctx, "greeting")
metrics := bus.Metrics() // Published, Delivered
```

Other adapters (e.g. Redis Streams, NATS, Kafka) can be built on top of the same interface.
