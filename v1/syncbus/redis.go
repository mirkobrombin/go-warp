package syncbus

import (
	"context"
	stdErrors "errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"

	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
)

const redisBusTimeout = 5 * time.Second

type redisSubscription struct {
	pubsub *redis.PubSub
	chans  []chan struct{}
}

// RedisBus implements Bus using a Redis backend.
type RedisBus struct {
	client    *redis.Client
	mu        sync.Mutex
	subs      map[string]*redisSubscription
	pending   map[string]struct{}
	processed map[string]struct{}
	published uint64
	delivered uint64
}

// NewRedisBus returns a new RedisBus using the provided Redis client.
func NewRedisBus(client *redis.Client) *RedisBus {
	return &RedisBus{
		client:    client,
		subs:      make(map[string]*redisSubscription),
		pending:   make(map[string]struct{}),
		processed: make(map[string]struct{}),
	}
}

// Publish implements Bus.Publish.
func (b *RedisBus) Publish(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
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
			if stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
				return warperrors.ErrTimeout
			}
			return ctx.Err()
		case <-time.After(time.Duration(j)):
		}
	}

	id := uuid.NewString()
	backoff := 100 * time.Millisecond
	var err error
	for {
		cctx, cancel := context.WithTimeout(ctx, redisBusTimeout)
		err = b.client.Publish(cctx, key, id).Err()
		cancel()
		if err == nil {
			atomic.AddUint64(&b.published, 1)
			break
		}
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		_ = b.reconnect()
		select {
		case <-ctx.Done():
			b.mu.Lock()
			delete(b.pending, key)
			b.mu.Unlock()
			if stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
				return warperrors.ErrTimeout
			}
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
	return nil
}

// Subscribe implements Bus.Subscribe.
func (b *RedisBus) Subscribe(ctx context.Context, key string) (chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}
	ch := make(chan struct{}, 1)
	backoff := 100 * time.Millisecond
	for {
		b.mu.Lock()
		sub, ok := b.subs[key]
		if ok {
			sub.chans = append(sub.chans, ch)
			b.mu.Unlock()
			break
		}
		b.mu.Unlock()
		cctx, cancel := context.WithTimeout(ctx, redisBusTimeout)
		ps := b.client.Subscribe(cctx, key)
		_, err := ps.Receive(cctx)
		cancel()
		if err == nil {
			b.mu.Lock()
			sub = &redisSubscription{pubsub: ps, chans: []chan struct{}{ch}}
			b.subs[key] = sub
			b.mu.Unlock()
			go b.dispatch(key, sub)
			break
		}
		_ = ps.Close()
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		_ = b.reconnect()
		select {
		case <-ctx.Done():
			if stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, warperrors.ErrTimeout
			}
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

func (b *RedisBus) dispatch(key string, sub *redisSubscription) {
	for msg := range sub.pubsub.Channel() {
		id := msg.Payload
		b.mu.Lock()
		if _, ok := b.processed[id]; ok {
			b.mu.Unlock()
			continue
		}
		b.processed[id] = struct{}{}
		chans := append([]chan struct{}(nil), sub.chans...)
		b.mu.Unlock()
		for _, ch := range chans {
			select {
			case ch <- struct{}{}:
				atomic.AddUint64(&b.delivered, 1)
			default:
			}
		}
	}
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *RedisBus) Unsubscribe(ctx context.Context, key string, ch chan struct{}) error {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
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
		cctx, cancel := context.WithTimeout(ctx, redisBusTimeout)
		defer cancel()
		_ = sub.pubsub.Unsubscribe(cctx, key)
		if err := sub.pubsub.Close(); err != nil {
			if stdErrors.Is(err, redis.ErrClosed) {
				return warperrors.ErrConnectionClosed
			}
			return err
		}
		return nil
	}
	b.mu.Unlock()
	return nil
}

// RevokeLease publishes a lease revocation event.
func (b *RedisBus) RevokeLease(ctx context.Context, id string) error {
	return b.Publish(ctx, "lease:"+id)
}

// SubscribeLease subscribes to lease revocation events.
func (b *RedisBus) SubscribeLease(ctx context.Context, id string) (chan struct{}, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *RedisBus) UnsubscribeLease(ctx context.Context, id string, ch chan struct{}) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

// Metrics returns the published and delivered counts.
func (b *RedisBus) Metrics() Metrics {
	return Metrics{
		Published: atomic.LoadUint64(&b.published),
		Delivered: atomic.LoadUint64(&b.delivered),
	}
}

func (b *RedisBus) reconnect() error {
	if b.client != nil && b.client.Ping(context.Background()).Err() == nil {
		return nil
	}
	opts := b.client.Options()
	b.client = redis.NewClient(opts)
	b.mu.Lock()
	for key, sub := range b.subs {
		_ = sub.pubsub.Close()
		ps := b.client.Subscribe(context.Background(), key)
		_, _ = ps.Receive(context.Background())
		sub.pubsub = ps
		go b.dispatch(key, sub)
	}
	b.mu.Unlock()
	return nil
}
