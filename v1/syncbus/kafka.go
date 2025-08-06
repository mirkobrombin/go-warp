package syncbus

import (
	"context"
	"sync"
	"sync/atomic"

	sarama "github.com/IBM/sarama"
)

type kafkaSubscription struct {
	pc    sarama.PartitionConsumer
	chans []chan struct{}
}

// KafkaBus implements Bus using a Kafka backend.
type KafkaBus struct {
	producer  sarama.SyncProducer
	consumer  sarama.Consumer
	mu        sync.Mutex
	subs      map[string]*kafkaSubscription
	pending   map[string]struct{}
	published uint64
	delivered uint64
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
func (b *KafkaBus) Publish(ctx context.Context, key string) error {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil // deduplicate
	}
	b.pending[key] = struct{}{}
	b.mu.Unlock()

	msg := &sarama.ProducerMessage{Topic: key, Value: sarama.StringEncoder("1")}
	if _, _, err := b.producer.SendMessage(msg); err != nil {
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
		return err
	}
	atomic.AddUint64(&b.published, 1)
	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()
	return nil
}

// Subscribe implements Bus.Subscribe.
func (b *KafkaBus) Subscribe(ctx context.Context, key string) (chan struct{}, error) {
	ch := make(chan struct{}, 1)
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
		chans := append([]chan struct{}(nil), b.subs[key].chans...)
		b.mu.Unlock()
		for _, ch := range chans {
			select {
			case ch <- struct{}{}:
				atomic.AddUint64(&b.delivered, 1)
			default:
			}
		}
	}
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *KafkaBus) Unsubscribe(ctx context.Context, key string, ch chan struct{}) error {
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

// Metrics returns the published and delivered counts.
func (b *KafkaBus) Metrics() Metrics {
	return Metrics{
		Published: atomic.LoadUint64(&b.published),
		Delivered: atomic.LoadUint64(&b.delivered),
	}
}

// Close releases resources used by the KafkaBus.
func (b *KafkaBus) Close() {
	_ = b.producer.Close()
	_ = b.consumer.Close()
}
