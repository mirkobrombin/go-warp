package nats

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	nats "github.com/nats-io/nats.go"
)

type natsSubscription struct {
	sub   *nats.Subscription
	chans []chan syncbus.Event
}

// NATSBus implements Bus using a NATS backend.
type NATSBus struct {
	conn      *nats.Conn
	mu        sync.Mutex
	subs      map[string]*natsSubscription
	pending   map[string]struct{}
	processed map[string]struct{}
	published atomic.Uint64
	delivered atomic.Uint64
}

// NewNATSBus returns a new NATSBus using the provided connection.
func NewNATSBus(conn *nats.Conn) *NATSBus {
	return &NATSBus{
		conn:      conn,
		subs:      make(map[string]*natsSubscription),
		pending:   make(map[string]struct{}),
		processed: make(map[string]struct{}),
	}
}

// Publish implements Bus.Publish.
func (b *NATSBus) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil // deduplicate
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

	id := uuid.NewString()
	backoff := 100 * time.Millisecond
	var err error
	for {
		err = b.conn.Publish(key, []byte(id))
		if err == nil {
			b.published.Add(1)
			break
		}
		_ = b.reconnect()
		select {
		case <-ctx.Done():
			b.mu.Lock()
			delete(b.pending, key)
			b.mu.Unlock()
			return ctx.Err()
		default:
		}
		jitter := time.Duration(rand.Int63n(int64(backoff)))
		time.Sleep(backoff + jitter)
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
	}

	time.AfterFunc(time.Millisecond, func() {
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
	})
	return err
}

// PublishAndAwait implements Bus.PublishAndAwait. NATS core subjects do not expose
// subscriber counts, so only a quorum of 1 is supported.
func (b *NATSBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	if replicas <= 0 {
		replicas = 1
	}
	if replicas > 1 {
		return syncbus.ErrQuorumUnsupported
	}
	if err := b.Publish(ctx, key, opts...); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			return ctx.Err()
		}
		return b.conn.FlushTimeout(timeout)
	}
	return b.conn.Flush()
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology.
func (b *NATSBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	// NATS does not support topology awareness in Core NATS.
	return syncbus.ErrQuorumUnsupported
}

// Subscribe implements Bus.Subscribe.
func (b *NATSBus) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	ch := make(chan syncbus.Event, 1)
	backoff := 100 * time.Millisecond

	for {
		b.mu.Lock()
		sub := b.subs[key]
		b.mu.Unlock()
		if sub != nil {
			b.mu.Lock()
			sub.chans = append(sub.chans, ch)
			b.mu.Unlock()
			break
		}
		ns, err := b.conn.Subscribe(key, b.natsHandler(key))
		if err == nil {
			b.mu.Lock()
			b.subs[key] = &natsSubscription{sub: ns, chans: []chan syncbus.Event{ch}}
			b.mu.Unlock()
			break
		}
		_ = b.reconnect()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		jitter := time.Duration(rand.Int63n(int64(backoff)))
		time.Sleep(backoff + jitter)
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
	}

	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()
	return ch, nil
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *NATSBus) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
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

// IsHealthy implements Bus.IsHealthy.
func (b *NATSBus) IsHealthy() bool {
	return true
}

// SubscribeLease subscribes to lease revocation events.
func (b *NATSBus) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *NATSBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

// Metrics returns the published and delivered counts.
func (b *NATSBus) Metrics() syncbus.Metrics {
	return syncbus.Metrics{
		Published: b.published.Load(),
		Delivered: b.delivered.Load(),
	}
}

func (b *NATSBus) natsHandler(key string) nats.MsgHandler {
	return func(m *nats.Msg) {
		id := string(m.Data)
		b.mu.Lock()
		if _, ok := b.processed[id]; ok {
			b.mu.Unlock()
			return
		}
		b.processed[id] = struct{}{}
		chans := append([]chan syncbus.Event(nil), b.subs[key].chans...)
		b.mu.Unlock()

		evt := syncbus.Event{Key: key}
		for _, c := range chans {
			select {
			case c <- evt:
				b.delivered.Add(1)
			default:
			}
		}
	}
}

func (b *NATSBus) reconnect() error {
	if b.conn != nil && b.conn.IsConnected() {
		return nil
	}
	newConn, err := b.conn.Opts.Connect()
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.conn = newConn
	for key, sub := range b.subs {
		ns, err := b.conn.Subscribe(key, b.natsHandler(key))
		if err != nil {
			continue
		}
		sub.sub = ns
	}
	b.mu.Unlock()
	return nil
}
