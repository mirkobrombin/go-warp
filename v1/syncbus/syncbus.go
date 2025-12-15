package syncbus

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQuorumUnsupported  = errors.New("syncbus: quorum unsupported")
	ErrQuorumNotSatisfied = errors.New("syncbus: quorum not satisfied")
)

// Scope defines the propagation scope of an event.
type Scope uint8

const (
	ScopeLocal Scope = iota
	ScopeGlobal
)

type PublishOptions struct {
	Region      string
	VectorClock map[string]uint64
	Scope       Scope
}

type PublishOption func(*PublishOptions)

func WithRegion(region string) PublishOption {
	return func(o *PublishOptions) {
		o.Region = region
	}
}

func WithVectorClock(vc map[string]uint64) PublishOption {
	return func(o *PublishOptions) {
		o.VectorClock = vc
	}
}

func WithScope(scope Scope) PublishOption {
	return func(o *PublishOptions) {
		o.Scope = scope
	}
}

// Event represents a bus event carrying metadata.
type Event struct {
	Key         string
	Region      string
	VectorClock map[string]uint64
	Scope       Scope
}

// Bus provides a simple pub/sub mechanism used by warp to propagate
// invalidation events across nodes.
type Bus interface {
	Publish(ctx context.Context, key string, opts ...PublishOption) error
	PublishAndAwait(ctx context.Context, key string, replicas int, opts ...PublishOption) error
	PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...PublishOption) error
	Subscribe(ctx context.Context, key string) (<-chan Event, error)
	Unsubscribe(ctx context.Context, key string, ch <-chan Event) error
	RevokeLease(ctx context.Context, id string) error
	SubscribeLease(ctx context.Context, id string) (<-chan Event, error)
	UnsubscribeLease(ctx context.Context, id string, ch <-chan Event) error
	IsHealthy() bool
}

// InMemoryBus is a local implementation of Bus mainly for testing.
type InMemoryBus struct {
	mu        sync.Mutex
	subs      map[string][]chan Event
	pending   map[string]struct{}
	published atomic.Uint64
	delivered atomic.Uint64
}

// NewInMemoryBus returns a new InMemoryBus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{subs: make(map[string][]chan Event), pending: make(map[string]struct{})}
}

// IsHealthy implements Bus.IsHealthy.
func (b *InMemoryBus) IsHealthy() bool {
	return true
}

// Publish implements Bus.Publish.
func (b *InMemoryBus) Publish(ctx context.Context, key string, opts ...PublishOption) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	options := PublishOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil // deduplicate
	}
	b.pending[key] = struct{}{}
	chans := append([]chan Event(nil), b.subs[key]...)
	b.mu.Unlock()

	// random small delay to reduce publish bursts
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

	evt := Event{
		Key:         key,
		Region:      options.Region,
		VectorClock: options.VectorClock,
	}

	b.published.Add(1)
	for _, ch := range chans {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			delete(b.pending, key)
			b.mu.Unlock()
			return ctx.Err()
		default:
		}
		select {
		case ch <- evt:
			b.delivered.Add(1)
		default:
		}
	}

	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()
	return nil
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology.
func (b *InMemoryBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...PublishOption) error {
	// In-memory simulation: treat zones as simple replica count for now
	return b.PublishAndAwait(ctx, key, minZones, opts...)
}

// PublishAndAwait implements Bus.PublishAndAwait.
func (b *InMemoryBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...PublishOption) error {
	if replicas <= 0 {
		replicas = 1
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	options := PublishOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil
	}
	chans := append([]chan Event(nil), b.subs[key]...)
	if len(chans) < replicas {
		b.mu.Unlock()
		return ErrQuorumNotSatisfied
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

	evt := Event{
		Key:         key,
		Region:      options.Region,
		VectorClock: options.VectorClock,
	}

	delivered := 0
	for _, ch := range chans {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			delete(b.pending, key)
			b.mu.Unlock()
			return ctx.Err()
		default:
		}
		select {
		case ch <- evt:
			b.delivered.Add(1)
			delivered++
		default:
		}
	}

	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()

	if delivered < replicas {
		return ErrQuorumNotSatisfied
	}
	b.published.Add(1)
	return nil
}

// Subscribe implements Bus.Subscribe.
func (b *InMemoryBus) Subscribe(ctx context.Context, key string) (<-chan Event, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ch := make(chan Event, 1)
	b.mu.Lock()
	b.subs[key] = append(b.subs[key], ch)
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()
	return ch, nil
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *InMemoryBus) Unsubscribe(ctx context.Context, key string, ch <-chan Event) error {
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

// RevokeLease publishes a lease revocation event.
func (b *InMemoryBus) RevokeLease(ctx context.Context, id string) error {
	return b.Publish(ctx, "lease:"+id)
}

// SubscribeLease subscribes to lease revocation events.
func (b *InMemoryBus) SubscribeLease(ctx context.Context, id string) (<-chan Event, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *InMemoryBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan Event) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

type Metrics struct {
	Published uint64
	Delivered uint64
}

func (b *InMemoryBus) Metrics() Metrics {
	return Metrics{
		Published: b.published.Load(),
		Delivered: b.delivered.Load(),
	}
}
