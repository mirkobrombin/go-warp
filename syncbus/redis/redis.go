package redis

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	warperrors "github.com/mirkobrombin/go-warp/v2/errors"
	"github.com/mirkobrombin/go-warp/v2/syncbus"
	redis "github.com/redis/go-redis/v9"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	redisBusTimeout     = 5 * time.Second
	globalUpdateChannel = "warp:updates"
	batchThreshold      = 10
	batchTicker         = 100 * time.Millisecond
)

type BatchPayload struct {
	Nonce   string              `json:"n"` // Unique per batch to prevent false dedup
	Keys    []string            `json:"k"`
	Regions []string            `json:"r,omitempty"`
	Vectors []map[string]uint64 `json:"v,omitempty"`
	Scopes  []syncbus.Scope     `json:"s,omitempty"`
}

type redisSubscription struct {
	pubsub *redis.PubSub
	chans  []chan syncbus.Event
}

type publishReq struct {
	ctx  context.Context
	key  string
	opts syncbus.PublishOptions
	resp chan error
}

// RedisBus implements Bus using a Redis backend with batching support.
type RedisBus struct {
	client *redis.Client
	region string
	mu     sync.Mutex
	subs   map[string]*redisSubscription

	pending      map[string]struct{}
	seen         map[string]time.Time
	published    atomic.Uint64
	delivered    atomic.Uint64
	publishCh    chan publishReq
	closeCh      chan struct{}
	globalReady  chan struct{}
	globalSub    *redis.PubSub
	globalCancel context.CancelFunc
	wg           sync.WaitGroup
}

// RedisBusOptions configures the RedisBus.
type RedisBusOptions struct {
	Client *redis.Client
	Region string
}

// NewRedisBus returns a new RedisBus using the provided client.
func NewRedisBus(opts RedisBusOptions) *RedisBus {
	b := &RedisBus{
		client:      opts.Client,
		region:      opts.Region,
		subs:        make(map[string]*redisSubscription),
		pending:     make(map[string]struct{}),
		seen:        make(map[string]time.Time),
		publishCh:   make(chan publishReq, 1000),
		closeCh:     make(chan struct{}),
		globalReady: make(chan struct{}),
	}
	b.wg.Add(3)
	go b.runBatcher()
	go b.dispatchGlobal()
	go b.cleanupSeen()
	return b
}

// Publish implements Bus.Publish.
func (b *RedisBus) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	options := syncbus.PublishOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil
	}
	b.pending[key] = struct{}{}
	b.mu.Unlock()

	cleanup := func() {
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
	}

	resp := make(chan error, 1)
	select {
	case b.publishCh <- publishReq{ctx: ctx, key: key, opts: options, resp: resp}:
		select {
		case err := <-resp:
			cleanup()
			if err == nil {
				b.published.Add(1)
			}
			return err
		case <-ctx.Done():
			cleanup()
			if ctx.Err() == context.DeadlineExceeded {
				return warperrors.ErrTimeout
			}
			return ctx.Err()
		}
	case <-ctx.Done():
		cleanup()
		if ctx.Err() == context.DeadlineExceeded {
			return warperrors.ErrTimeout
		}
		return ctx.Err()
	}
}

// PublishAndAwait implements Bus.PublishAndAwait using Redis publish counts.
func (b *RedisBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	if replicas <= 0 {
		replicas = 1
	}

	b.mu.Lock()
	client := b.client
	b.mu.Unlock()
	val, err := client.Publish(ctx, key, uuid.NewString()).Result()
	if err != nil {
		return err
	}
	if int(val) < replicas {
		return syncbus.ErrQuorumNotSatisfied
	}
	b.published.Add(1)
	return nil
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology.
func (b *RedisBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	return b.PublishAndAwait(ctx, key, minZones, opts...)
}

// Subscribe implements Bus.Subscribe.
func (b *RedisBus) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	ch := make(chan syncbus.Event, 1)
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := b.subs[key]
	if sub == nil {
		ps := b.client.Subscribe(ctx, key)
		if _, err := ps.Receive(ctx); err != nil {
			return nil, err
		}
		sub = &redisSubscription{pubsub: ps, chans: []chan syncbus.Event{ch}}
		b.subs[key] = sub
		go b.dispatch(key, sub)
	} else {
		sub.chans = append(sub.chans, ch)
	}

	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()
	return ch, nil
}

func (b *RedisBus) checkSeen(payload string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.seen[payload]; ok {
		return true
	}
	b.seen[payload] = time.Now()
	return false
}

func (b *RedisBus) cleanupSeen() {
	defer b.wg.Done()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			now := time.Now()
			for k, t := range b.seen {
				if now.Sub(t) > 1*time.Minute {
					delete(b.seen, k)
				}
			}
			b.mu.Unlock()
		case <-b.closeCh:
			return
		}
	}
}

func (b *RedisBus) dispatch(key string, sub *redisSubscription) {
	ch := sub.pubsub.Channel()
	for msg := range ch { // Loop terminates when channel is closed
		if b.checkSeen(msg.Payload) {
			continue
		}

		evt := syncbus.Event{Key: key}
		b.mu.Lock()
		for _, c := range sub.chans {
			select {
			case c <- evt:
				b.delivered.Add(1)
			default:
			}
		}
		b.mu.Unlock()
	}
}

func (b *RedisBus) dispatchGlobal() {
	defer b.wg.Done()
	b.mu.Lock()
	client := b.client
	b.mu.Unlock()

	// Retry the subscription until it succeeds or the context ends.
	for {
		// Refresh client in case of reconnection
		b.mu.Lock()
		client = b.client
		b.mu.Unlock()

		receiveCtx, cancel := context.WithCancel(context.Background())
		ps := client.Subscribe(receiveCtx, globalUpdateChannel)
		b.mu.Lock()
		if client != b.client {
			b.mu.Unlock()
			cancel()
			_ = ps.Close()
			continue
		}
		b.globalSub = ps
		b.globalCancel = cancel
		b.mu.Unlock()
		_, err := ps.ReceiveTimeout(receiveCtx, redisBusTimeout)
		cancel()
		b.mu.Lock()
		if b.globalSub == ps {
			b.globalCancel = nil
		}
		b.mu.Unlock()
		if err != nil {
			_ = ps.Close()
			b.mu.Lock()
			if b.globalSub == ps {
				b.globalSub = nil
			}
			b.mu.Unlock()
			select {
			case <-b.closeCh:
				return
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}

		b.mu.Lock()
		if client == b.client {
			select {
			case <-b.globalReady:
			default:
				close(b.globalReady)
			}
		}
		b.mu.Unlock()

		ch := ps.Channel()

	loop:
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					// Channel closed, connection lost
					break loop
				}
				if msg == nil {
					continue
				}
				if b.checkSeen(msg.Payload) {
					continue
				}

				// Decode payload
				reader := bytes.NewReader([]byte(msg.Payload))
				gz, err := gzip.NewReader(reader)
				if err != nil {
					continue
				}
				var batch BatchPayload
				if err := json.NewDecoder(gz).Decode(&batch); err != nil {
					gz.Close()
					continue
				}
				gz.Close()

				for i, key := range batch.Keys {
					b.mu.Lock()
					sub, ok := b.subs[key]
					if !ok {
						b.mu.Unlock()
						continue
					}
					evt := syncbus.Event{
						Key: key,
					}
					if i < len(batch.Regions) {
						evt.Region = batch.Regions[i]
					}
					if i < len(batch.Vectors) {
						evt.VectorClock = batch.Vectors[i]
					}
					if i < len(batch.Scopes) {
						evt.Scope = batch.Scopes[i]
					}

					for _, c := range sub.chans {
						select {
						case c <- evt:
							b.delivered.Add(1)
						default:
						}
					}
					b.mu.Unlock()
				}

			case <-b.closeCh:
				ps.Close()
				return
			}
		}

		ps.Close()
		b.mu.Lock()
		if b.globalSub == ps {
			b.globalSub = nil
		}
		b.mu.Unlock()

		// If we are here, connection was lost. Wait a bit and try to get new client.
		select {
		case <-b.closeCh:
			return
		case <-time.After(10 * time.Millisecond): // Reduced delay
			// Refresh client pointer
			b.mu.Lock()
			client = b.client
			b.mu.Unlock()
		}
	}
}

func (b *RedisBus) readyClient() (*redis.Client, error) {
	timeout := time.NewTimer(redisBusTimeout)
	defer timeout.Stop()
	probe := time.NewTicker(10 * time.Millisecond)
	defer probe.Stop()

	for {
		b.mu.Lock()
		client := b.client
		ready := b.globalReady
		b.mu.Unlock()

		select {
		case <-ready:
			b.mu.Lock()
			current := ready == b.globalReady
			client = b.client
			b.mu.Unlock()
			if current {
				return client, nil
			}
		case <-probe.C:
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			err := client.Ping(ctx).Err()
			cancel()
			if err != nil {
				return nil, err
			}
		case <-b.closeCh:
			return nil, context.Canceled
		case <-timeout.C:
			return nil, warperrors.ErrTimeout
		}
	}
}

func (b *RedisBus) runBatcher() {
	defer b.wg.Done()
	ticker := time.NewTicker(batchTicker)
	defer ticker.Stop()

	var batch []publishReq

	flush := func() {
		if len(batch) == 0 {
			return
		}

		// Deduplicate keys in the batch
		seen := make(map[string]struct{})
		uniqueBatch := make([]publishReq, 0, len(batch))

		for _, req := range batch {
			if _, ok := seen[req.key]; !ok {
				seen[req.key] = struct{}{}
				uniqueBatch = append(uniqueBatch, req)
			}
		}

		payload := BatchPayload{
			Nonce:   uuid.NewString(), // Ensure payload is unique even for same keys
			Keys:    make([]string, len(uniqueBatch)),
			Regions: make([]string, len(uniqueBatch)),
			Vectors: make([]map[string]uint64, len(uniqueBatch)),
			Scopes:  make([]syncbus.Scope, len(uniqueBatch)),
		}

		for i, req := range uniqueBatch {
			payload.Keys[i] = req.key
			payload.Regions[i] = req.opts.Region
			payload.Vectors[i] = req.opts.VectorClock
			payload.Scopes[i] = req.opts.Scope
		}

		// Compress and send
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if err := json.NewEncoder(gz).Encode(payload); err != nil {
			// Fail all requests
			for _, req := range batch {
				req.resp <- err
			}
			batch = nil
			gz.Close() // Ensure gzip writer is closed even on encode error
			return
		}
		gz.Close()

		client, err := b.readyClient()
		if err == nil {
			err = client.Publish(context.Background(), globalUpdateChannel, buf.String()).Err()
		}

		// If error (e.g. closed), try to reconnect once
		if err != nil {
			select {
			case <-b.closeCh:
				for _, req := range batch {
					req.resp <- err
				}
				batch = nil
				return
			default:
			}

			err = b.reconnect()
			if err == nil {
				client, err = b.readyClient()
			}

			// Add jitter to increase deduplication window and reduce stampedes
			if err == nil {
				if j := rand.Int63n(int64(100 * time.Millisecond)); j > 0 {
					select {
					case <-b.closeCh:
						return
					case <-time.After(time.Duration(j)):
					}
				}
				err = client.Publish(context.Background(), globalUpdateChannel, buf.String()).Err()
			}
		}

		for _, req := range batch {
			req.resp <- err
		}
		batch = nil
	}

	for {
		select {
		case req := <-b.publishCh:
			batch = append(batch, req)
			if len(batch) >= batchThreshold {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-b.closeCh:
			flush() // Flush remaining
			return
		}
	}
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *RedisBus) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := b.subs[key]
	if sub == nil {
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
		if sub.pubsub != nil {
			return sub.pubsub.Close()
		}
	}
	return nil
}

// RevokeLease publishes a lease revocation event.
func (b *RedisBus) RevokeLease(ctx context.Context, id string) error {
	return b.Publish(ctx, "lease:"+id)
}

// SubscribeLease subscribes to lease revocation events.
func (b *RedisBus) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *RedisBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

// Metrics returns the published and delivered counts.
func (b *RedisBus) Metrics() syncbus.Metrics {
	return syncbus.Metrics{
		Published: b.published.Load(),
		Delivered: b.delivered.Load(),
	}
}

// reconnect establishes a new connection/subscription (stub for tests).
func (b *RedisBus) reconnect() error {
	// In this implementation using go-redis, the client handles reconnection mostly.
	// But we might need to resubscribe.
	// For tests that close the client, we need to try to recreate it if we have options.
	// Note: b.client might be closed, so Options() might behave weirdly if internal state is gone,
	// but usually Options() just returns the config struct.

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.client != nil {
		if b.globalCancel != nil {
			b.globalCancel()
			b.globalCancel = nil
		}
		if b.globalSub != nil {
			_ = b.globalSub.Close()
			b.globalSub = nil
		}
		opts := b.client.Options()
		b.client = redis.NewClient(opts)
		b.globalReady = make(chan struct{})
	}

	for key, sub := range b.subs {
		if sub.pubsub != nil {
			_ = sub.pubsub.Close()
		}
		ps := b.client.Subscribe(context.Background(), key)
		// Wait for subscription to be established to avoid race with Publish
		if _, err := ps.Receive(context.Background()); err != nil {
			_ = ps.Close()
			continue
		}
		sub.pubsub = ps
		go b.dispatch(key, sub)
	}
	return nil
}

// IsHealthy implements Bus.IsHealthy.
func (b *RedisBus) IsHealthy() bool {
	return true
}

// Peers implements Bus.Peers.
func (b *RedisBus) Peers() []string {
	return nil
}

// Close releases resources used by the RedisBus.
func (b *RedisBus) Close() error {
	close(b.closeCh)

	b.mu.Lock()
	var subs []*redis.PubSub
	if b.globalCancel != nil {
		b.globalCancel()
		b.globalCancel = nil
	}
	if b.globalSub != nil {
		subs = append(subs, b.globalSub)
		b.globalSub = nil
	}
	for _, sub := range b.subs {
		if sub.pubsub != nil {
			subs = append(subs, sub.pubsub)
		}
	}
	// Clear the map to ensure no further operations can use the subscriptions
	b.subs = make(map[string]*redisSubscription)
	b.mu.Unlock()

	for _, ps := range subs {
		_ = ps.Close()
	}

	b.wg.Wait()
	return nil
}
