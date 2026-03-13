package watchbus

import (
	"context"
	"strings"
	"sync"

	nats "github.com/nats-io/nats.go"
)

// NATSWatchBus implements WatchBus using NATS Core subjects.
type NATSWatchBus struct {
	conn          *nats.Conn
	mu            sync.Mutex
	watches       map[string]map[chan []byte]*nats.Subscription
	prefixWatches map[string]map[chan []byte]*nats.Subscription
}

// NewNATSWatchBus creates a new NATSWatchBus using the provided connection.
func NewNATSWatchBus(conn *nats.Conn) *NATSWatchBus {
	return &NATSWatchBus{
		conn:          conn,
		watches:       make(map[string]map[chan []byte]*nats.Subscription),
		prefixWatches: make(map[string]map[chan []byte]*nats.Subscription),
	}
}

// Publish sends data to all NATS subscribers of key.
func (b *NATSWatchBus) Publish(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return b.conn.Publish(key, data)
}

// PublishPrefix publishes data to all watched keys that start with prefix.
// It iterates the in-process watch index and publishes to each matching key;
// prefix subscribers receive the message via NATS wildcard routing.
func (b *NATSWatchBus) PublishPrefix(ctx context.Context, prefix string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	b.mu.Lock()
	var keys []string
	for k := range b.watches {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	b.mu.Unlock()
	for _, k := range keys {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := b.conn.Publish(k, data); err != nil {
			return err
		}
	}
	return nil
}

// Watch subscribes to key and returns a channel receiving messages until the
// context is canceled or Unwatch is called.
func (b *NATSWatchBus) Watch(ctx context.Context, key string) (chan []byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	ch := make(chan []byte, 1)
	sub, err := b.conn.Subscribe(key, func(msg *nats.Msg) {
		select {
		case ch <- msg.Data:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	m := b.watches[key]
	if m == nil {
		m = make(map[chan []byte]*nats.Subscription)
		b.watches[key] = m
	}
	m[ch] = sub
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), key, ch)
	}()
	return ch, nil
}

// SubscribePrefix subscribes to all keys with the given prefix using the NATS
// wildcard subject prefix + ".>" (matches any subject one or more tokens deeper).
func (b *NATSWatchBus) SubscribePrefix(ctx context.Context, prefix string) (chan []byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	ch := make(chan []byte, 1)
	sub, err := b.conn.Subscribe(prefix+".>", func(msg *nats.Msg) {
		select {
		case ch <- msg.Data:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	m := b.prefixWatches[prefix]
	if m == nil {
		m = make(map[chan []byte]*nats.Subscription)
		b.prefixWatches[prefix] = m
	}
	m[ch] = sub
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), prefix, ch)
	}()
	return ch, nil
}

// Unwatch stops delivering messages for key to ch, unsubscribes from NATS,
// and closes the channel.
func (b *NATSWatchBus) Unwatch(ctx context.Context, key string, ch chan []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	b.mu.Lock()
	if m, ok := b.watches[key]; ok {
		if sub, ok := m[ch]; ok {
			delete(m, ch)
			if len(m) == 0 {
				delete(b.watches, key)
			}
			b.mu.Unlock()
			close(ch)
			return sub.Unsubscribe()
		}
	}
	if m, ok := b.prefixWatches[key]; ok {
		if sub, ok := m[ch]; ok {
			delete(m, ch)
			if len(m) == 0 {
				delete(b.prefixWatches, key)
			}
			b.mu.Unlock()
			close(ch)
			return sub.Unsubscribe()
		}
	}
	b.mu.Unlock()
	return nil
}
