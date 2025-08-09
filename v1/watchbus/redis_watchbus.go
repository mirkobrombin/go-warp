package watchbus

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisWatchBus uses Redis Streams to implement WatchBus.
type RedisWatchBus struct {
	client  *redis.Client
	mu      sync.Mutex
	cancels map[string]map[chan []byte]context.CancelFunc
}

// NewRedisWatchBus creates a new RedisWatchBus using the provided client.
func NewRedisWatchBus(client *redis.Client) *RedisWatchBus {
	return &RedisWatchBus{client: client, cancels: make(map[string]map[chan []byte]context.CancelFunc)}
}

// Publish adds a new message to the Redis stream identified by key.
func (b *RedisWatchBus) Publish(ctx context.Context, key string, data []byte) error {
	return b.client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"data": data}}).Err()
}

// Watch reads messages from the Redis stream.
func (b *RedisWatchBus) Watch(ctx context.Context, key string) (chan []byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan []byte, 1)

	b.mu.Lock()
	m := b.cancels[key]
	if m == nil {
		m = make(map[chan []byte]context.CancelFunc)
		b.cancels[key] = m
	}
	m[ch] = cancel
	b.mu.Unlock()

	go func() {
		defer close(ch)
		lastID := "$"
		for {
			res, err := b.client.XRead(ctx, &redis.XReadArgs{
				Streams: []string{key, lastID},
				Block:   0,
				Count:   1,
			}).Result()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(time.Second)
				continue
			}
			for _, s := range res {
				for _, msg := range s.Messages {
					lastID = msg.ID
					if v, ok := msg.Values["data"].(string); ok {
						select {
						case ch <- []byte(v):
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}
	}()

	return ch, nil
}

// Unwatch stops watching the given key and channel.
func (b *RedisWatchBus) Unwatch(ctx context.Context, key string, ch chan []byte) error {
	b.mu.Lock()
	if m, ok := b.cancels[key]; ok {
		if cancel, ok := m[ch]; ok {
			delete(m, ch)
			if len(m) == 0 {
				delete(b.cancels, key)
			}
			b.mu.Unlock()
			cancel()
			return nil
		}
	}
	b.mu.Unlock()
	return nil
}
