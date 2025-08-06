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

## NATS Bus

`NATSBus` uses [NATS](https://nats.io/) subjects (one per key) to propagate events:

```go
// connect using nats.go
nc, _ := nats.Connect("nats://localhost:4222")
bus := syncbus.NewNATSBus(nc)
ch, _ := bus.Subscribe(ctx, "greeting")
go func() { for range ch { fmt.Println("invalidated") } }()
_ = bus.Publish(ctx, "greeting")
```

## Kafka Bus

`KafkaBus` publishes to Kafka topics named after each key and consumes from partition 0:

```go
cfg := sarama.NewConfig()
cfg.Version = sarama.V2_0_0_0
bus, _ := syncbus.NewKafkaBus([]string{"localhost:9092"}, cfg)
ch, _ := bus.Subscribe(ctx, "greeting")
go func() { for range ch { fmt.Println("invalidated") } }()
_ = bus.Publish(ctx, "greeting")
```

Other adapters (e.g. Redis Streams) can be built on top of the same interface.
