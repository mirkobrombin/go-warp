package syncbus

import (
	"context"
	"sync"
	"sync/atomic"

	redis "github.com/redis/go-redis/v9"
)

type redisSubscription struct {
	pubsub *redis.PubSub
	chans  []chan struct{}
}

// RedisBus implements Bus using a Redis backend.
type RedisBus struct {
	client    *redis.Client
	mu        sync.Mutex
	subs      map[string]*redisSubscription
	pending   map[string]struct{}
	published uint64
	delivered uint64
}

// NewRedisBus returns a new RedisBus using the provided Redis client.
func NewRedisBus(client *redis.Client) *RedisBus {
	return &RedisBus{
		client:  client,
		subs:    make(map[string]*redisSubscription),
		pending: make(map[string]struct{}),
	}
}

// Publish implements Bus.Publish.
func (b *RedisBus) Publish(ctx context.Context, key string) {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return // deduplicate
	}
	b.pending[key] = struct{}{}
	b.mu.Unlock()

	_ = b.client.Publish(ctx, key, "1").Err()
	atomic.AddUint64(&b.published, 1)

	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()
}

// Subscribe implements Bus.Subscribe.
func (b *RedisBus) Subscribe(ctx context.Context, key string) (chan struct{}, error) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	sub, ok := b.subs[key]
	if !ok {
		ps := b.client.Subscribe(ctx, key)
		sub = &redisSubscription{pubsub: ps}
		b.subs[key] = sub
		go b.dispatch(sub)
	}
	sub.chans = append(sub.chans, ch)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()
	return ch, nil
}

func (b *RedisBus) dispatch(sub *redisSubscription) {
	for range sub.pubsub.Channel() {
		b.mu.Lock()
		chans := append([]chan struct{}(nil), sub.chans...)
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
func (b *RedisBus) Unsubscribe(ctx context.Context, key string, ch chan struct{}) error {
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
		_ = sub.pubsub.Unsubscribe(ctx, key)
		return sub.pubsub.Close()
	}
	b.mu.Unlock()
	return nil
}

// Metrics returns the published and delivered counts.
func (b *RedisBus) Metrics() Metrics {
	return Metrics{
		Published: atomic.LoadUint64(&b.published),
		Delivered: atomic.LoadUint64(&b.delivered),
	}
}
