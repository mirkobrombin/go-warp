package syncbus

import (
	"context"
	"sync"
	"sync/atomic"

	nats "github.com/nats-io/nats.go"
)

type natsSubscription struct {
	sub   *nats.Subscription
	chans []chan struct{}
}

// NATSBus implements Bus using a NATS backend.
type NATSBus struct {
	conn      *nats.Conn
	mu        sync.Mutex
	subs      map[string]*natsSubscription
	pending   map[string]struct{}
	published uint64
	delivered uint64
}

// NewNATSBus returns a new NATSBus using the provided connection.
func NewNATSBus(conn *nats.Conn) *NATSBus {
	return &NATSBus{
		conn:    conn,
		subs:    make(map[string]*natsSubscription),
		pending: make(map[string]struct{}),
	}
}

// Publish implements Bus.Publish.
func (b *NATSBus) Publish(ctx context.Context, key string) error {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil // deduplicate
	}
	b.pending[key] = struct{}{}
	b.mu.Unlock()

	err := b.conn.Publish(key, []byte("1"))
	if err == nil {
		atomic.AddUint64(&b.published, 1)
	}

	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()
	return err
}

// Subscribe implements Bus.Subscribe.
func (b *NATSBus) Subscribe(ctx context.Context, key string) (chan struct{}, error) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	sub := b.subs[key]
	if sub == nil {
		ns, err := b.conn.Subscribe(key, func(_ *nats.Msg) {
			b.mu.Lock()
			chans := append([]chan struct{}(nil), b.subs[key].chans...)
			b.mu.Unlock()
			for _, c := range chans {
				select {
				case c <- struct{}{}:
					atomic.AddUint64(&b.delivered, 1)
				default:
				}
			}
		})
		if err != nil {
			b.mu.Unlock()
			return nil, err
		}
		sub = &natsSubscription{sub: ns}
		b.subs[key] = sub
	}
	sub.chans = append(sub.chans, ch)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()
	return ch, nil
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *NATSBus) Unsubscribe(ctx context.Context, key string, ch chan struct{}) error {
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
		return sub.sub.Unsubscribe()
	}
	b.mu.Unlock()
	return nil
}

// RevokeLease publishes a lease revocation event.
func (b *NATSBus) RevokeLease(ctx context.Context, id string) error {
	return b.Publish(ctx, "lease:"+id)
}

// SubscribeLease subscribes to lease revocation events.
func (b *NATSBus) SubscribeLease(ctx context.Context, id string) (chan struct{}, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *NATSBus) UnsubscribeLease(ctx context.Context, id string, ch chan struct{}) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

// Metrics returns the published and delivered counts.
func (b *NATSBus) Metrics() Metrics {
	return Metrics{
		Published: atomic.LoadUint64(&b.published),
		Delivered: atomic.LoadUint64(&b.delivered),
	}
}
