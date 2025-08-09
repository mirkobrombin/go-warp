package watchbus

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisWatchBus uses Redis Streams to implement WatchBus.
type RedisWatchBus struct {
	client        *redis.Client
	mu            sync.Mutex
	cancels       map[string]map[chan []byte]context.CancelFunc
	prefixCancels map[string]map[chan []byte]context.CancelFunc
}

// NewRedisWatchBus creates a new RedisWatchBus using the provided client.
func NewRedisWatchBus(client *redis.Client) *RedisWatchBus {
	return &RedisWatchBus{
		client:        client,
		cancels:       make(map[string]map[chan []byte]context.CancelFunc),
		prefixCancels: make(map[string]map[chan []byte]context.CancelFunc),
	}
}

// Publish adds a new message to the Redis stream identified by key.
func (b *RedisWatchBus) Publish(ctx context.Context, key string, data []byte) error {
	if err := b.client.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"data": data}}).Err(); err != nil {
		return err
	}
	return b.client.Publish(ctx, key, data).Err()
}

// PublishPrefix publishes the message to all keys having the given prefix.
func (b *RedisWatchBus) PublishPrefix(ctx context.Context, prefix string, data []byte) error {
	var cursor uint64
	for {
		keys, next, err := b.client.SScan(ctx, "watchbus:index", cursor, prefix+"*", 100).Result()
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Publish(ctx, k, data); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return b.client.Publish(ctx, prefix, data).Err()
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
	if len(m) == 1 {
		_ = b.client.SAdd(context.Background(), "watchbus:index", key).Err()
	}
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

// SubscribePrefix subscribes to all channels matching the given prefix.
func (b *RedisWatchBus) SubscribePrefix(ctx context.Context, prefix string) (chan []byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan []byte, 1)

	ps := b.client.PSubscribe(ctx, prefix+"*")
	b.mu.Lock()
	m := b.prefixCancels[prefix]
	if m == nil {
		m = make(map[chan []byte]context.CancelFunc)
		b.prefixCancels[prefix] = m
	}
	m[ch] = func() {
		cancel()
		_ = ps.Close()
	}
	b.mu.Unlock()

	go func() {
		defer close(ch)
		for {
			msg, err := ps.ReceiveMessage(ctx)
			if err != nil {
				return
			}
			select {
			case ch <- []byte(msg.Payload):
			case <-ctx.Done():
				return
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
				_ = b.client.SRem(context.Background(), "watchbus:index", key).Err()
			}
			b.mu.Unlock()
			cancel()
			return nil
		}
	}
	if m, ok := b.prefixCancels[key]; ok {
		if cancel, ok := m[ch]; ok {
			delete(m, ch)
			if len(m) == 0 {
				delete(b.prefixCancels, key)
			}
			b.mu.Unlock()
			cancel()
			return nil
		}
	}
	b.mu.Unlock()
	return nil
}
