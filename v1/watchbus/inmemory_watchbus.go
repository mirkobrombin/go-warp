package watchbus

import (
	"context"
	"sync"
)

// InMemoryWatchBus is an in-memory implementation of WatchBus.
type InMemoryWatchBus struct {
	mu         sync.Mutex
	subs       map[string][]chan []byte
	prefixSubs map[string][]chan []byte
	index      map[string]map[chan []byte]struct{}
}

// NewInMemory creates a new InMemoryWatchBus.
func NewInMemory() *InMemoryWatchBus {
	return &InMemoryWatchBus{
		subs:       make(map[string][]chan []byte),
		prefixSubs: make(map[string][]chan []byte),
		index:      make(map[string]map[chan []byte]struct{}),
	}
}

// Publish sends data to all watchers of key.
func (b *InMemoryWatchBus) Publish(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	targets := make(map[chan []byte]struct{})
	b.mu.Lock()
	for _, ch := range b.subs[key] {
		targets[ch] = struct{}{}
	}
	for i := 1; i <= len(key); i++ {
		p := key[:i]
		for _, ch := range b.prefixSubs[p] {
			targets[ch] = struct{}{}
		}
	}
	b.mu.Unlock()

	for ch := range targets {
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

// PublishPrefix sends data to all watchers of keys with the given prefix.
func (b *InMemoryWatchBus) PublishPrefix(ctx context.Context, prefix string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	targets := make(map[chan []byte]struct{})
	b.mu.Lock()
	for ch := range b.index[prefix] {
		targets[ch] = struct{}{}
	}
	for _, ch := range b.prefixSubs[prefix] {
		targets[ch] = struct{}{}
	}
	b.mu.Unlock()

	for ch := range targets {
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
	for i := 1; i <= len(key); i++ {
		p := key[:i]
		m := b.index[p]
		if m == nil {
			m = make(map[chan []byte]struct{})
			b.index[p] = m
		}
		m[ch] = struct{}{}
	}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), key, ch)
	}()
	return ch, nil
}

// SubscribePrefix subscribes to all keys with the given prefix.
func (b *InMemoryWatchBus) SubscribePrefix(ctx context.Context, prefix string) (chan []byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ch := make(chan []byte, 1)
	b.mu.Lock()
	b.prefixSubs[prefix] = append(b.prefixSubs[prefix], ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), prefix, ch)
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
	removed := false
	for i, c := range subs {
		if c == ch {
			subs[i] = subs[len(subs)-1]
			subs = subs[:len(subs)-1]
			b.subs[key] = subs
			close(c)
			removed = true
			break
		}
	}
	if removed {
		if len(subs) == 0 {
			delete(b.subs, key)
		}
		for i := 1; i <= len(key); i++ {
			p := key[:i]
			if m, ok := b.index[p]; ok {
				delete(m, ch)
				if len(m) == 0 {
					delete(b.index, p)
				}
			}
		}
		b.mu.Unlock()
		return nil
	}
	// try prefix subscriptions
	subs = b.prefixSubs[key]
	for i, c := range subs {
		if c == ch {
			subs[i] = subs[len(subs)-1]
			subs = subs[:len(subs)-1]
			b.prefixSubs[key] = subs
			close(c)
			break
		}
	}
	if len(subs) == 0 {
		delete(b.prefixSubs, key)
	}
	b.mu.Unlock()
	return nil
}
