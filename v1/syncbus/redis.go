package syncbus

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	stdErrors "errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
)

const (
	redisBusTimeout     = 5 * time.Second
	globalUpdateChannel = "warp:updates"
	federationChannel   = "warp:federation"
	batchThreshold      = 10
)

var tracer = otel.Tracer("github.com/mirkobrombin/go-warp/v1/syncbus")

type BatchPayload struct {
	Keys      []string            `json:"k"`
	IDs       []string            `json:"i"`
	Timestamp int64               `json:"t"` // UnixMilli
	Regions   []string            `json:"r,omitempty"`
	Vectors   []map[string]uint64 `json:"v,omitempty"`
	Scopes    []Scope             `json:"s,omitempty"`
}

type redisSubscription struct {
	pubsub *redis.PubSub
	chans  []chan Event
}

// RedisBus implements Bus using a Redis backend.
type RedisBus struct {
	client     *redis.Client
	fedClient  *redis.Client // Dedicated client for federation (Relay)
	isGateway  bool
	currentReg string

	mu        sync.Mutex
	subs      map[string]*redisSubscription
	pending   map[string]struct{}
	processed map[string]struct{}
	published atomic.Uint64
	delivered atomic.Uint64
	publishCh chan publishReq
	batchSub  *redis.PubSub
	fedSub    *redis.PubSub
	closeCh   chan struct{}
}

type publishReq struct {
	ctx  context.Context
	key  string
	opts PublishOptions
	resp chan error
}

type RedisBusOptions struct {
	Client           *redis.Client
	FederationClient *redis.Client
	IsGateway        bool
	Region           string
}

// NewRedisBus returns a new RedisBus using the provided Redis client.
func NewRedisBus(opts RedisBusOptions) *RedisBus {
	b := &RedisBus{
		client:     opts.Client,
		fedClient:  opts.FederationClient,
		isGateway:  opts.IsGateway,
		currentReg: opts.Region,

		subs:      make(map[string]*redisSubscription),
		pending:   make(map[string]struct{}),
		processed: make(map[string]struct{}),
		publishCh: make(chan publishReq, 1000), // Buffer for high throughput
		closeCh:   make(chan struct{}),
	}

	// Subscribe to global updates channel for adaptive batching (local bus)
	b.batchSub = opts.Client.Subscribe(context.Background(), globalUpdateChannel)
	go b.dispatchGlobal()

	if b.isGateway && b.fedClient != nil {
		b.fedSub = b.fedClient.Subscribe(context.Background(), federationChannel)
		go b.runGatewayRelay()
	}

	go b.runBatcher()
	return b
}

// Close gracefully stops the bus and closes subscriptions.
func (b *RedisBus) Close() error {
	close(b.closeCh)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.batchSub != nil {
		_ = b.batchSub.Close()
	}
	if b.fedSub != nil {
		_ = b.fedSub.Close()
	}

	for _, sub := range b.subs {
		_ = sub.pubsub.Close()
		for _, ch := range sub.chans {
			close(ch)
		}
	}
	b.subs = make(map[string]*redisSubscription)
	return nil
}

func (b *RedisBus) runBatcher() {
	const maxBatchSize = 100
	const flushInterval = 10 * time.Millisecond

	var batch []publishReq
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}

		ctx := context.Background()
		useGlobalBatch := len(batch) >= batchThreshold

		var lastErr error

		// Adaptive Batching: Compress and send to global channel
		// Even if not using batching for EVERYTHING, we use it for structured events
		if useGlobalBatch {
			payload := BatchPayload{
				Timestamp: time.Now().UnixMilli(),
			}
			for _, req := range batch {
				payload.Keys = append(payload.Keys, req.key)
				payload.IDs = append(payload.IDs, uuid.NewString())
				payload.Regions = append(payload.Regions, req.opts.Region)
				payload.Vectors = append(payload.Vectors, req.opts.VectorClock)
				payload.Scopes = append(payload.Scopes, req.opts.Scope)
			}

			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			if err := json.NewEncoder(gz).Encode(payload); err == nil {
				gz.Close()
				data := buf.Bytes()

				for attempt := 0; attempt < 3; attempt++ {
					cctx, cancel := context.WithTimeout(ctx, redisBusTimeout)
					err := b.client.Publish(cctx, globalUpdateChannel, data).Err()
					cancel()

					if err == nil {
						for _, req := range batch {
							req.resp <- nil
						}
						batch = nil
						return
					}
					lastErr = err
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				lastErr = err
			}
		} else {
			// Standard Pipeline (Low Latency)
			for attempt := 0; attempt < 3; attempt++ {
				pipe := b.client.Pipeline()
				id := uuid.NewString()

				for _, req := range batch {
					// We must still respect the Global Scope logic.
					// However, standard pipeline only publishes ID to key.
					// For rich metadata/federation, we force usage of globalUpdateChannel
					// OR we need to accept that single-key Publish via simple Redis PubSub
					// loses metadata unless we change the protocol.
					// To fix this: For ScopeGlobal, we force batch path or specialized handling.
					if req.opts.Scope == ScopeGlobal {
						// Force batch path single item or handle separately?
						// Create a mini-batch for reuse logic
						// or publish to globalUpdateChannel directly.
						payload := BatchPayload{
							Timestamp: time.Now().UnixMilli(),
							Keys:      []string{req.key},
							IDs:       []string{id},
							Regions:   []string{req.opts.Region},
							Vectors:   []map[string]uint64{req.opts.VectorClock},
							Scopes:    []Scope{ScopeGlobal},
						}
						var buf bytes.Buffer
						gz := gzip.NewWriter(&buf)
						_ = json.NewEncoder(gz).Encode(payload)
						gz.Close()
						pipe.Publish(req.ctx, globalUpdateChannel, buf.Bytes())
					} else {
						pipe.Publish(req.ctx, req.key, id)
					}
				}

				fctx, cancel := context.WithTimeout(ctx, redisBusTimeout)
				_, err := pipe.Exec(fctx)
				cancel()

				if err == nil {
					for _, req := range batch {
						req.resp <- nil
					}
					batch = nil
					return
				}

				lastErr = err
				if recErr := b.reconnect(); recErr != nil {
					time.Sleep(50 * time.Millisecond)
				} else {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}

		for _, req := range batch {
			req.resp <- lastErr
		}
		batch = nil
	}

	for {
		select {
		case <-b.closeCh:
			flush()
			return
		case req := <-b.publishCh:
			batch = append(batch, req)
			if len(batch) >= maxBatchSize {
				flush()
				ticker.Reset(flushInterval)
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Publish implements Bus.Publish.
func (b *RedisBus) Publish(ctx context.Context, key string, opts ...PublishOption) error {
	ctx, span := tracer.Start(ctx, "RedisBus.Publish", trace.WithAttributes(attribute.String("warp.bus.key", key)))
	defer span.End()

	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
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
	b.mu.Unlock()

	resp := make(chan error, 1)
	select {
	case b.publishCh <- publishReq{ctx: ctx, key: key, opts: options, resp: resp}:
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
		return ctx.Err()
	}

	select {
	case err := <-resp:
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()

		if err == nil {
			b.published.Add(1)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PublishAndAwait implements Bus.PublishAndAwait using Redis publish counts.
func (b *RedisBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...PublishOption) error {
	ctx, span := tracer.Start(ctx, "RedisBus.PublishAndAwait", trace.WithAttributes(attribute.String("warp.bus.key", key), attribute.Int("warp.bus.replicas", replicas)))
	defer span.End()

	if replicas <= 0 {
		replicas = 1
	}
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil
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
	var (
		err       error
		delivered int64
	)
	for {
		cctx, cancel := context.WithTimeout(ctx, redisBusTimeout)
		cmd := b.client.Publish(cctx, key, id)
		delivered, err = cmd.Result()
		cancel()
		if err == nil {
			break
		}
		if stdErrors.Is(err, context.DeadlineExceeded) {
			b.mu.Lock()
			delete(b.pending, key)
			b.mu.Unlock()
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

	b.mu.Lock()
	delete(b.pending, key)
	b.mu.Unlock()

	if int(delivered) < replicas {
		return ErrQuorumNotSatisfied
	}
	b.published.Add(1)
	return nil
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology.
func (b *RedisBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...PublishOption) error {
	// Standard Redis Pub/Sub cannot verify zone topology implicitly.
	// In a real usage, this would require a custom status channel or an overlay.
	// For now, we fall back to replica count if we assume 1 replica per zone.
	// This is a "Best Effort" implementation for v1.
	return b.PublishAndAwait(ctx, key, minZones, opts...)
}

// Subscribe implements Bus.Subscribe.
func (b *RedisBus) Subscribe(ctx context.Context, key string) (<-chan Event, error) {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}
	ch := make(chan Event, 1)
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
			sub = &redisSubscription{pubsub: ps, chans: []chan Event{ch}}
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
	_, span := tracer.Start(context.Background(), "RedisBus.Dispatch", trace.WithAttributes(attribute.String("warp.bus.key", key)))
	defer span.End()

	for msg := range sub.pubsub.Channel() {
		id := msg.Payload
		b.mu.Lock()
		if _, ok := b.processed[id]; ok {
			b.mu.Unlock()
			continue
		}
		b.processed[id] = struct{}{}
		chans := append([]chan Event(nil), sub.chans...)
		b.mu.Unlock()

		evt := Event{Key: key} // Legacy event, empty metadata
		for _, ch := range chans {
			select {
			case ch <- evt:
				b.delivered.Add(1)
			default:
			}
		}
	}
}

func (b *RedisBus) dispatchGlobal() {
	for msg := range b.batchSub.Channel() {
		// Attempt decompression
		gz, err := gzip.NewReader(bytes.NewReader([]byte(msg.Payload)))
		if err != nil {
			continue
		}
		var payload BatchPayload
		if err := json.NewDecoder(gz).Decode(&payload); err != nil {
			gz.Close()
			continue
		}
		gz.Close()

		ctx := context.Background()
		_, span := tracer.Start(ctx, "RedisBus.DispatchBatch",
			trace.WithAttributes(
				attribute.Int("warp.bus.batch_size", len(payload.Keys)),
				attribute.Int64("warp.bus.propagation_latency_ms", time.Now().UnixMilli()-payload.Timestamp),
			))

		for i, key := range payload.Keys {
			if i >= len(payload.IDs) {
				break
			}
			id := payload.IDs[i]
			b.mu.Lock()
			if _, ok := b.processed[id]; ok {
				b.mu.Unlock()
				continue
			}
			b.processed[id] = struct{}{}

			var region string
			var vector map[string]uint64
			var scope Scope

			if i < len(payload.Regions) {
				region = payload.Regions[i]
			}
			if i < len(payload.Vectors) {
				vector = payload.Vectors[i]
			}
			if i < len(payload.Scopes) {
				scope = payload.Scopes[i]
			}

			// RELAY LOGIC (Outbound):
			// If we are a Gateway, and the event is Global, and assuming it originated here (or we are just relaying it),
			// we must forward it to the Federation Bus.
			// Ideally, we only relay if Origin Region == My Region (to avoid loops).
			// But currentReg might stick.
			if b.isGateway && b.fedClient != nil && scope == ScopeGlobal {
				if region == "" || region == b.currentReg {
					// Message from this instance, broadcast to global channel
					// We create a mini batch payload for federation to preserve metadata
					fedPayload := BatchPayload{
						Timestamp: time.Now().UnixMilli(),
						Keys:      []string{key},
						IDs:       []string{id},
						Regions:   []string{region}, // Keep origin
						Vectors:   []map[string]uint64{vector},
						Scopes:    []Scope{ScopeGlobal},
					}
					var buf bytes.Buffer
					gz := gzip.NewWriter(&buf)
					if err := json.NewEncoder(gz).Encode(fedPayload); err == nil {
						gz.Close()
						// Fire and forget to federation channel
						b.fedClient.Publish(ctx, federationChannel, buf.Bytes())
					}
				}
			}

			if sub, ok := b.subs[key]; ok {
				chans := append([]chan Event(nil), sub.chans...)
				b.mu.Unlock()

				evt := Event{
					Key:         key,
					Region:      region,
					VectorClock: vector,
					Scope:       scope,
				}

				for _, ch := range chans {
					select {
					case ch <- evt:
						b.delivered.Add(1)
					default:
					}
				}
			} else {
				b.mu.Unlock()
			}
		}
		span.End()
	}
}

// runGatewayRelay listens to the Federation Bus and relays valid events to the Local Bus.
func (b *RedisBus) runGatewayRelay() {
	for msg := range b.fedSub.Channel() {
		gz, err := gzip.NewReader(bytes.NewReader([]byte(msg.Payload)))
		if err != nil {
			continue
		}
		var payload BatchPayload
		if err := json.NewDecoder(gz).Decode(&payload); err != nil {
			gz.Close()
			continue
		}
		gz.Close()

		// INBOUND RELAY: Federation -> Local
		for _, id := range payload.IDs {
			// Check if we already processed this ID (loop prevention)
			b.mu.Lock()
			if _, ok := b.processed[id]; ok {
				b.mu.Unlock()
				continue
			}
			// We DO NOT mark it processed here yet, because we want dispatchGlobal to handle it
			// when it comes back from the Local Bus publish?
			// PRO: If we publish to Local Bus, we will receive it in dispatchGlobal.
			// CON: dispatchGlobal will try to Relay it back to Federation if we don't be careful!
			// FIX: In dispatchGlobal, we check "region == b.currentReg". If the inbound event
			// preserves the original region (e.g. "us-east"), and we are "eu-west",
			// dispatchGlobal will see region != currentReg and WON'T relay it back.
			b.mu.Unlock()

			// Republish to Local Bus (globalUpdateChannel) so all local nodes get it.
			// We must preserve metadata.
			// We can reuse the payload bytes since we just want to bridge it.
			// BUT: We need to make sure we don't lose the ID or change it.
			// Redis Publish is just raw bytes.
			b.client.Publish(context.Background(), globalUpdateChannel, []byte(msg.Payload))
		}
	}
}

// Unsubscribe implements Bus.Unsubscribe.
func (b *RedisBus) Unsubscribe(ctx context.Context, key string, ch <-chan Event) error {
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
func (b *RedisBus) SubscribeLease(ctx context.Context, id string) (<-chan Event, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease cancels a lease revocation subscription.
func (b *RedisBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan Event) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

// Metrics returns the published and delivered counts.
func (b *RedisBus) Metrics() Metrics {
	return Metrics{
		Published: b.published.Load(),
		Delivered: b.delivered.Load(),
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
