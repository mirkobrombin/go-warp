package syncbus

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/mirkobrombin/go-foundation/pkg/safemap"
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
	Peers() []string
}

// InMemoryBus is a local implementation of Bus mainly for testing.
type InMemoryBus struct {
	subs      *safemap.ShardedMap[string, []chan Event]
	pending   *safemap.ShardedMap[string, bool]
	published atomic.Uint64
	delivered atomic.Uint64
}

// NewInMemoryBus returns a new InMemoryBus.
func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{
		subs:    safemap.NewSharded[string, []chan Event](safemap.StringHasher, 32),
		pending: safemap.NewSharded[string, bool](safemap.StringHasher, 32),
	}
}

// IsHealthy implements Bus.IsHealthy.
func (b *InMemoryBus) IsHealthy() bool {
	return true
}

// Peers implements Bus.Peers.
func (b *InMemoryBus) Peers() []string {
	return nil
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

	// Check and Set Pending (Deduplication)
	var alreadyPending bool
	b.pending.Compute(key, func(v bool, exists bool) bool {
		alreadyPending = exists
		return true
	})

	if alreadyPending {
		return nil
	}
	defer b.pending.Delete(key)

	chans, _ := b.subs.Get(key)
	if len(chans) == 0 {
		return nil
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
			return ctx.Err()
		case ch <- evt:
			b.delivered.Add(1)
		default:
		}
	}

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

	var alreadyPending bool
	b.pending.Compute(key, func(v bool, exists bool) bool {
		alreadyPending = exists
		return true
	})

	if alreadyPending {
		return nil
	}
	defer b.pending.Delete(key)

	chans, _ := b.subs.Get(key)
	if len(chans) < replicas {
		return ErrQuorumNotSatisfied
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
			return ctx.Err()
		case ch <- evt:
			b.delivered.Add(1)
			delivered++
		default:
		}
	}

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

	ch := make(chan Event, 1) // Buffer 1
	b.subs.Compute(key, func(v []chan Event, exists bool) []chan Event {
		return append(v, ch)
	})

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

	b.subs.Compute(key, func(subs []chan Event, exists bool) []chan Event {
		if !exists {
			return nil
		}
		for i, c := range subs {
			if c == ch {
				// Remove (swap with last)
				subs[i] = subs[len(subs)-1]
				subs = subs[:len(subs)-1]
				close(c)
				break
			}
		}
		if len(subs) == 0 {
			return subs
		}
		return subs
	})

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
