package watchbus

import (
	"context"
	"sync"
)

// InMemoryWatchBus is an in-memory implementation of WatchBus.
type InMemoryWatchBus struct {
	mu   sync.Mutex
	subs map[string][]chan []byte
}

// NewInMemory creates a new InMemoryWatchBus.
func NewInMemory() *InMemoryWatchBus {
	return &InMemoryWatchBus{subs: make(map[string][]chan []byte)}
}

// Publish sends data to all watchers of key.
func (b *InMemoryWatchBus) Publish(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.mu.Lock()
	chans := append([]chan []byte(nil), b.subs[key]...)
	b.mu.Unlock()
	for _, ch := range chans {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		select {
		case ch <- data:
		default:
		}
	}
	return nil
}

// Watch subscribes to key and returns a channel receiving messages.
func (b *InMemoryWatchBus) Watch(ctx context.Context, key string) (chan []byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ch := make(chan []byte, 1)
	b.mu.Lock()
	b.subs[key] = append(b.subs[key], ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), key, ch)
	}()
	return ch, nil
}

// Unwatch removes the channel from key watchers.
func (b *InMemoryWatchBus) Unwatch(ctx context.Context, key string, ch chan []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.mu.Lock()
	subs := b.subs[key]
	for i, c := range subs {
		if c == ch {
			subs[i] = subs[len(subs)-1]
			subs = subs[:len(subs)-1]
			b.subs[key] = subs
			close(c)
			break
		}
	}
	if len(subs) == 0 {
		delete(b.subs, key)
	}
	b.mu.Unlock()
	return nil
}
