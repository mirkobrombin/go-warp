# Sync Bus

The `syncbus` module propagates invalidation events and coordinates distributed operations.

## API Reference

### Constructors

#### `NewInMemoryBus`
```go
func NewInMemoryBus() *InMemoryBus
```
Creates a local bus. Useful for testing or single-node async architectures.

#### `NewNATSBus`
```go
func NewNATSBus(nc *nats.Conn, subject string) *NATSBus
```
Wraps a NATS connection.
- **nc**: Connected NATS client.
- **subject**: NATS subject to publish/subscribe to (e.g., "warp.events").

#### `NewKafkaBus`
```go
func NewKafkaBus(producer sarama.SyncProducer, consumer sarama.ConsumerGroup, topic string) *KafkaBus
```
Wraps a Sarama Kafka producer/consumer.

#### `NewRedisBus`
```go
func NewRedisBus(client *redis.Client, channel string) *RedisBus
```
Wraps a Go-Redis client using Pub/Sub.

### `Bus` Interface

```go
type Bus interface {
    // Publish broadcasts an invalidation for key.
    Publish(ctx context.Context, key string) error

    // PublishAndAwait broadcasts and waits for 'replicas' acknowledgements.
    // Returns ErrQuorumNotSatisfied if timeout/failure.
    PublishAndAwait(ctx context.Context, key string, replicas int) error

    // Subscribe returns a channel that receives events for key.
    Subscribe(ctx context.Context, key string) (chan struct{}, error)

    // Unsubscribe stops listening.
    Unsubscribe(ctx context.Context, key string, ch chan struct{}) error

    // Lease methods
    RevokeLease(ctx context.Context, id string) error
    SubscribeLease(ctx context.Context, id string) (chan struct{}, error)
    UnsubscribeLease(ctx context.Context, id string, ch chan struct{}) error
}
```

### `Metrics`

```go
type Metrics struct {
    Published uint64
    Delivered uint64
}

func (b *InMemoryBus) Metrics() Metrics
```
Returns statistics about published and delivered messages (InMemoryBus only).

### Errors

- **`ErrQuorumUnsupported`**: Returned if the bus implementation does not support quorum (e.g., simple pub/sub).
- **`ErrQuorumNotSatisfied`**: Returned by `PublishAndAwait` if the required number of acknowledgements is not received.

## How It Works

When you perform a `Set` or `Invalidate` operation in a distributed mode:

1. Warp publishes an event to the bus.
2. The payload contains the `key` and the operation type.
3. All other nodes subscribed to the bus receive the event.
4. They apply the invalidation locally (removing the key from their L1 cache).
