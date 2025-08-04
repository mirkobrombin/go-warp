package syncbus

import (
	"context"
	"sync"
	"sync/atomic"
)

// Bus provides a simple pub/sub mechanism used by warp to propagate
// invalidation events across nodes.
type Bus interface {
	Publish(ctx context.Context, key string)
	Subscribe(ctx context.Context, key string) (<-chan struct{}, error)
}

// InMemoryBus is a local implementation of Bus mainly for testing.
type InMemoryBus struct {
	mu        sync.Mutex
	subs      map[string][]chan struct{}
	pending   map[string]struct{}
	published uint64
	delivered uint64
}

// NewInMemoryBus returns a new InMemoryBus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{subs: make(map[string][]chan struct{}), pending: make(map[string]struct{})}
}

// Publish implements Bus.Publish.
func (b *InMemoryBus) Publish(ctx context.Context, key string) {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return // deduplicate
	}
	b.pending[key] = struct{}{}
	chans := append([]chan struct{}(nil), b.subs[key]...)
	b.mu.Unlock()
	atomic.AddUint64(&b.published, 1)
	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
			atomic.AddUint64(&b.delivered, 1)
		default:
		}
	}
	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()
}

// Subscribe implements Bus.Subscribe.
func (b *InMemoryBus) Subscribe(ctx context.Context, key string) (<-chan struct{}, error) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[key] = append(b.subs[key], ch)
	b.mu.Unlock()
	return ch, nil
}

type Metrics struct {
	Published uint64
	Delivered uint64
}

func (b *InMemoryBus) Metrics() Metrics {
	return Metrics{
		Published: atomic.LoadUint64(&b.published),
		Delivered: atomic.LoadUint64(&b.delivered),
	}
}
