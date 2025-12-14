package kafka

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

type kafkaSubscription struct {
	pc    sarama.PartitionConsumer
	chans []chan syncbus.Event
}

// KafkaBus implements Bus using a Kafka backend.
type KafkaBus struct {
	producer  sarama.SyncProducer
	consumer  sarama.Consumer
	mu        sync.Mutex
	subs      map[string]*kafkaSubscription
	pending   map[string]struct{}
	published atomic.Uint64
	delivered atomic.Uint64
}

// NewKafkaBus creates a new KafkaBus connecting to the given brokers.
func NewKafkaBus(brokers []string, cfg *sarama.Config) (*KafkaBus, error) {
	if !cfg.Producer.Return.Successes {
		cfg.Producer.Return.Successes = true
	}
	client, err := sarama.NewClient(brokers, cfg)
	if err != nil {
		return nil, err
	}
	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		_ = producer.Close()
		_ = client.Close()
		return nil, err
	}
	return &KafkaBus{
		producer: producer,
		consumer: consumer,
		subs:     make(map[string]*kafkaSubscription),
		pending:  make(map[string]struct{}),
	}, nil
}

// Publish implements Bus.Publish.
func (b *KafkaBus) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil // deduplicate
	}
	b.pending[key] = struct{}{}
	b.mu.Unlock()

	if j := rand.Int63n(int64(10 * time.Millisecond)); j > 0 {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			delete(b.pending, key)
			b.mu.Unlock()
			return ctx.Err()
		case <-time.After(time.Duration(j)):
		}
	}

	msg := &sarama.ProducerMessage{Topic: key, Value: sarama.StringEncoder("1")}
	if _, _, err := b.producer.SendMessage(msg); err != nil {
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
		return err
	}
	b.published.Add(1)
	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()
	return nil
}

// PublishAndAwait implements Bus.PublishAndAwait. Kafka sync producer does not expose
// subscriber replication acknowledgements at this level, so quorum is unsupported.
func (b *KafkaBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	if replicas <= 1 {
		return b.Publish(ctx, key, opts...)
	}
	return syncbus.ErrQuorumUnsupported
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology.
func (b *KafkaBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	// Kafka does not easily support this synchronous topology check.
	return syncbus.ErrQuorumUnsupported
}

// Subscribe implements Bus.Subscribe.
func (b *KafkaBus) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	ch := make(chan syncbus.Event, 1)
	b.mu.Lock()
	sub := b.subs[key]
	if sub == nil {
		pc, err := b.consumer.ConsumePartition(key, 0, sarama.OffsetNewest)
		if err != nil {
			b.mu.Unlock()
			return nil, err
		}
		sub = &kafkaSubscription{pc: pc}
		b.subs[key] = sub
		go b.dispatch(sub, key)
	}
	sub.chans = append(sub.chans, ch)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()
	return ch, nil
}

func (b *KafkaBus) dispatch(sub *kafkaSubscription, key string) {
	for range sub.pc.Messages() {
		b.mu.Lock()
		chans := append([]chan syncbus.Event(nil), b.subs[key].chans...)
		b.mu.Unlock()

		evt := syncbus.Event{Key: key}
		for _, ch := range chans {
			select {
			case ch <- evt:
				b.delivered.Add(1)
			default:
			}
		}
	}
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *KafkaBus) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
	b.mu.Lock()
	sub := b.subs[key]
	if sub == nil {
		b.mu.Unlock()
		return nil
	}
	for i, c := range sub.chans {
		if c == ch {
			sub.chans[i] = sub.chans[len(sub.chans)-1]
			sub.chans = sub.chans[:len(sub.chans)-1]
			close(c)
			break
		}
	}
	if len(sub.chans) == 0 {
		delete(b.subs, key)
		b.mu.Unlock()
		return sub.pc.Close()
	}
	b.mu.Unlock()
	return nil
}

// RevokeLease publishes a lease revocation event.
func (b *KafkaBus) RevokeLease(ctx context.Context, id string) error {
	return b.Publish(ctx, "lease:"+id)
}

// SubscribeLease subscribes to lease revocation events.
func (b *KafkaBus) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *KafkaBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

// Metrics returns the published and delivered counts.
func (b *KafkaBus) Metrics() syncbus.Metrics {
	return syncbus.Metrics{
		Published: b.published.Load(),
		Delivered: b.delivered.Load(),
	}
}

// Close releases resources used by the KafkaBus.
func (b *KafkaBus) Close() error {
	_ = b.producer.Close()
	_ = b.consumer.Close()
	return nil
}
