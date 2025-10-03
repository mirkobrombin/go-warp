package core

import (
	"context"
	"errors"
	"hash/fnv"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/metrics"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	"github.com/mirkobrombin/go-warp/v1/validator"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

var tracer = otel.Tracer("github.com/mirkobrombin/go-warp/v1/core")

// Mode represents the consistency mode for a key. See docs/core.md for the full mode table.
type Mode int

const (
	ModeStrongLocal Mode = iota
	ModeEventualDistributed
	ModeStrongDistributed
)

type registration struct {
	mu          sync.Mutex
	ttl         time.Duration
	ttlStrategy cache.TTLStrategy
	ttlOpts     cache.TTLOptions
	mode        Mode
	currentTTL  time.Duration
	lastAccess  time.Time
}

type regShard struct {
	sync.RWMutex
	regs map[string]*registration
}

const regShardCount = 32

// Warp orchestrates the interaction between cache, merge engine and sync bus.
type Warp[T any] struct {
	cache  cache.Cache[merge.Value[T]]
	store  adapter.Store[T]
	bus    syncbus.Bus
	merges *merge.Engine[T]
	leases *LeaseManager[T]

	publishErrCh chan error

	shards [regShardCount]regShard

	hitCounter      prometheus.Counter
	missCounter     prometheus.Counter
	evictionCounter prometheus.Counter
	latencyHist     prometheus.Histogram
}

// Txn represents a batch of operations to be applied atomically.
type Txn[T any] struct {
	w       *Warp[T]
	ctx     context.Context
	sets    map[string]T
	deletes map[string]struct{}
	cas     map[string]T
}

// versionedCache extends Cache with the ability to retrieve values at a specific time.
type versionedCache[T any] interface {
	cache.Cache[merge.Value[T]]
	GetAt(ctx context.Context, key string, at time.Time) (merge.Value[T], bool, error)
}

// Option configures a Warp instance.
type Option[T any] func(*Warp[T])

// WithMetrics enables Prometheus metrics collection for core operations.
func WithMetrics[T any](reg prometheus.Registerer) Option[T] {
	return func(w *Warp[T]) {
		w.hitCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_core_hits_total",
			Help: "Total number of cache hits",
		})
		w.missCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_core_misses_total",
			Help: "Total number of cache misses",
		})
		w.evictionCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_core_evictions_total",
			Help: "Total number of evictions",
		})
		w.latencyHist = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "warp_core_latency_seconds",
			Help:    "Latency of core operations",
			Buckets: prometheus.DefBuckets,
		})
		reg.MustRegister(w.hitCounter, w.missCounter, w.evictionCounter, w.latencyHist)
	}
}

func (w *Warp[T]) shard(key string) *regShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &w.shards[h.Sum32()%regShardCount]
}

func (w *Warp[T]) getReg(key string) (*registration, bool) {
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	return reg, ok
}

// PublishErrors exposes a channel where asynchronous publish errors are sent.
func (w *Warp[T]) PublishErrors() <-chan error {
	return w.publishErrCh
}

// New creates a new Warp instance.
func New[T any](c cache.Cache[merge.Value[T]], s adapter.Store[T], bus syncbus.Bus, m *merge.Engine[T], opts ...Option[T]) *Warp[T] {
	if m == nil {
		m = merge.NewEngine[T]()
	}
	w := &Warp[T]{
		cache:        c,
		store:        s,
		bus:          bus,
		merges:       m,
		publishErrCh: make(chan error, 1),
	}
	for i := 0; i < regShardCount; i++ {
		w.shards[i].regs = make(map[string]*registration)
	}
	w.leases = newLeaseManager[T](w, bus)
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Register registers a key with a specific mode and TTL. Mode must be chosen from the table in docs/core.md.
// Optional cache.TTLOption can enable sliding expiration or dynamic TTL
// adjustments. It returns false if the key was already registered.
func (w *Warp[T]) Register(key string, mode Mode, ttl time.Duration, opts ...cache.TTLOption) bool {
	shard := w.shard(key)
	shard.Lock()
	defer shard.Unlock()
	if _, exists := shard.regs[key]; exists {
		return false
	}
	var to cache.TTLOptions
	for _, opt := range opts {
		opt(&to)
	}
	shard.regs[key] = &registration{
		ttl:        ttl,
		ttlOpts:    to,
		mode:       mode,
		currentTTL: ttl,
	}
	return true
}

// RegisterDynamicTTL registers a key with a consistency mode and a dynamic TTL
// strategy. Mode options are documented in docs/core.md. It returns false if the key was already registered.
func (w *Warp[T]) RegisterDynamicTTL(key string, mode Mode, strat cache.TTLStrategy, opts ...cache.TTLOption) bool {
	shard := w.shard(key)
	shard.Lock()
	defer shard.Unlock()
	if _, exists := shard.regs[key]; exists {
		return false
	}
	var to cache.TTLOptions
	for _, opt := range opts {
		opt(&to)
	}
	shard.regs[key] = &registration{ttlStrategy: strat, ttlOpts: to, mode: mode}
	return true
}

// Unregister removes a key registration.
func (w *Warp[T]) Unregister(key string) {
	shard := w.shard(key)
	shard.Lock()
	delete(shard.regs, key)
	shard.Unlock()
}

// ErrNotFound is returned when a key does not exist in the cache.
var ErrNotFound = errors.New("warp: not found")

// ErrUnregistered is returned when a key is not registered.
var ErrUnregistered = errors.New("warp: key not registered")

// ErrCASMismatch is returned when the expected value differs from the current one.
var ErrCASMismatch = errors.New("warp: cas mismatch")

// Get retrieves a value from the cache.
func (w *Warp[T]) Get(ctx context.Context, key string) (T, error) {
	metrics.GetCounter.Inc()
	ctx, span := tracer.Start(ctx, "Warp.Get")
	start := time.Now()
	defer func() {
		span.SetAttributes(attribute.Int64("warp.core.latency_ms", time.Since(start).Milliseconds()))
		span.End()
		if w.latencyHist != nil {
			w.latencyHist.Observe(time.Since(start).Seconds())
		}
	}()
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		var zero T
		return zero, ErrUnregistered
	}
	if reg.ttlStrategy != nil {
		reg.ttlStrategy.Record(key)
	}
	if v, ok, err := w.cache.Get(ctx, key); err != nil {
		var zero T
		return zero, err
	} else if ok {
		now := time.Now()
		if reg.ttlStrategy != nil {
			if reg.ttlOpts.Sliding || reg.ttlOpts.FreqThreshold > 0 {
				ttl := reg.ttlStrategy.TTL(key)
				if reg.ttlOpts.FreqThreshold > 0 {
					reg.mu.Lock()
					ttl = reg.ttlOpts.Adjust(ttl, reg.lastAccess, now)
					reg.currentTTL = ttl
					reg.lastAccess = now
					reg.mu.Unlock()
				} else {
					reg.mu.Lock()
					reg.currentTTL = ttl
					reg.lastAccess = now
					reg.mu.Unlock()
				}
				_ = w.cache.Set(ctx, key, v, ttl)
			}
		} else if reg.ttlOpts.Sliding || reg.ttlOpts.FreqThreshold > 0 {
			reg.mu.Lock()
			ttl := reg.ttlOpts.Adjust(reg.currentTTL, reg.lastAccess, now)
			reg.currentTTL = ttl
			reg.lastAccess = now
			reg.mu.Unlock()
			_ = w.cache.Set(ctx, key, v, ttl)
		}
		if w.hitCounter != nil {
			w.hitCounter.Inc()
		}
		span.SetAttributes(attribute.String("warp.core.result", "hit"))
		return v.Data, nil
	} else {
		if w.missCounter != nil {
			w.missCounter.Inc()
		}
		span.SetAttributes(attribute.String("warp.core.result", "miss"))
	}
	if w.store != nil {
		v, ok, err := w.store.Get(ctx, key)
		if err != nil {
			var zero T
			return zero, err
		}
		if ok {
			now := time.Now()
			mv := merge.Value[T]{Data: v, Timestamp: now}
			ttl := reg.ttl
			if reg.ttlStrategy != nil {
				ttl = reg.ttlStrategy.TTL(key)
			}
			reg.mu.Lock()
			reg.currentTTL = ttl
			reg.lastAccess = now
			reg.mu.Unlock()
			_ = w.cache.Set(ctx, key, mv, ttl)
			return mv.Data, nil
		}
	}
	var zero T
	return zero, ErrNotFound
}

// GetAt retrieves the value for a key at the specified time, if available.
func (w *Warp[T]) GetAt(ctx context.Context, key string, at time.Time) (T, error) {
	metrics.GetCounter.Inc()
	ctx, span := tracer.Start(ctx, "Warp.GetAt")
	start := time.Now()
	defer func() {
		span.SetAttributes(attribute.Int64("warp.core.latency_ms", time.Since(start).Milliseconds()))
		span.End()
		if w.latencyHist != nil {
			w.latencyHist.Observe(time.Since(start).Seconds())
		}
	}()
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		var zero T
		return zero, ErrUnregistered
	}
	if reg.ttlStrategy != nil {
		reg.ttlStrategy.Record(key)
	}
	vc, ok := w.cache.(versionedCache[T])
	if !ok {
		var zero T
		return zero, ErrNotFound
	}
	if v, ok, err := vc.GetAt(ctx, key, at); err != nil {
		var zero T
		return zero, err
	} else if ok {
		if w.hitCounter != nil {
			w.hitCounter.Inc()
		}
		span.SetAttributes(attribute.String("warp.core.result", "hit"))
		return v.Data, nil
	}
	if w.missCounter != nil {
		w.missCounter.Inc()
	}
	span.SetAttributes(attribute.String("warp.core.result", "miss"))
	var zero T
	return zero, ErrNotFound
}

// Set stores a value in the cache, applying merge strategies and publishing if needed.
// It returns an error if persisting the value to the underlying store fails.
func (w *Warp[T]) Set(ctx context.Context, key string, value T) error {
	metrics.SetCounter.Inc()
	ctx, span := tracer.Start(ctx, "Warp.Set")
	start := time.Now()
	defer func() {
		span.SetAttributes(attribute.Int64("warp.core.latency_ms", time.Since(start).Milliseconds()))
		span.End()
		if w.latencyHist != nil {
			w.latencyHist.Observe(time.Since(start).Seconds())
		}
	}()
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		return ErrUnregistered
	}
	if reg.ttlStrategy != nil {
		reg.ttlStrategy.Record(key)
	}

	now := time.Now()
	newVal := merge.Value[T]{Data: value, Timestamp: now}
	if old, ok, err := w.cache.Get(ctx, key); err != nil {
		return err
	} else if ok {
		merged, err := w.merges.Merge(key, old, newVal)
		if err == nil {
			newVal = merged
		}
	}

	ttl := reg.ttl
	if reg.ttlStrategy != nil {
		ttl = reg.ttlStrategy.TTL(key)
	}
	reg.mu.Lock()
	reg.currentTTL = ttl
	reg.lastAccess = now
	reg.mu.Unlock()
	if err := w.cache.Set(ctx, key, newVal, ttl); err != nil {
		return err
	}
	if w.store != nil {
		if err := w.store.Set(ctx, key, newVal.Data); err != nil {
			return err
		}
	}

	if reg.mode != ModeStrongLocal && w.bus != nil {
		go func() {
			if err := w.bus.Publish(context.WithoutCancel(ctx), key); err != nil {
				select {
				case w.publishErrCh <- err:
				default:
				}
			}
		}()
	}

	return nil
}

// Invalidate removes a key and propagates the invalidation if required.
// It returns an error if removing the key from the cache fails.
func (w *Warp[T]) Invalidate(ctx context.Context, key string) error {
	metrics.InvalidateCounter.Inc()
	ctx, span := tracer.Start(ctx, "Warp.Invalidate")
	start := time.Now()
	defer func() {
		span.SetAttributes(attribute.Int64("warp.core.latency_ms", time.Since(start).Milliseconds()))
		span.End()
		if w.latencyHist != nil {
			w.latencyHist.Observe(time.Since(start).Seconds())
		}
	}()
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		return ErrUnregistered
	}
	if err := w.cache.Invalidate(ctx, key); err != nil {
		return err
	}
	if w.evictionCounter != nil {
		w.evictionCounter.Inc()
	}
	if reg.mode != ModeStrongLocal && w.bus != nil {
		go func() {
			if err := w.bus.Publish(context.WithoutCancel(ctx), key); err != nil {
				select {
				case w.publishErrCh <- err:
				default:
				}
			}
		}()
	}
	return nil
}

// Merge registers a custom merge function for a key.
func (w *Warp[T]) Merge(key string, fn merge.MergeFn[T]) {
	w.merges.Register(key, fn)
}

// Warmup loads registered keys from the storage into the cache.
func (w *Warp[T]) Warmup(ctx context.Context) {
	if w.store == nil {
		return
	}
	keys := make([]string, 0)
	for i := 0; i < regShardCount; i++ {
		shard := &w.shards[i]
		shard.RLock()
		for k := range shard.regs {
			keys = append(keys, k)
		}
		shard.RUnlock()
	}

	type res struct {
		key string
		val T
	}

	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, runtime.NumCPU())
	ch := make(chan res, len(keys))

	for _, k := range keys {
		k := k
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return gctx.Err()
			}
			defer func() { <-sem }()
			v, ok, err := w.store.Get(gctx, k)
			if err != nil || !ok {
				return nil
			}
			select {
			case <-gctx.Done():
				return gctx.Err()
			case ch <- res{key: k, val: v}:
				return nil
			}
		})
	}

	go func() {
		_ = g.Wait()
		close(ch)
	}()

	for r := range ch {
		if gctx.Err() != nil {
			break
		}
		shard := w.shard(r.key)
		shard.RLock()
		reg := shard.regs[r.key]
		shard.RUnlock()
		now := time.Now()
		mv := merge.Value[T]{Data: r.val, Timestamp: now}
		ttl := reg.ttl
		if reg.ttlStrategy != nil {
			ttl = reg.ttlStrategy.TTL(r.key)
		}
		reg.mu.Lock()
		reg.currentTTL = ttl
		reg.lastAccess = now
		reg.mu.Unlock()
		_ = w.cache.Set(ctx, r.key, mv, ttl)
	}
}

// GrantLease creates a new lease with the given TTL.
func (w *Warp[T]) GrantLease(ctx context.Context, ttl time.Duration) (string, error) {
	return w.leases.Grant(ctx, ttl)
}

// RevokeLease stops a lease and propagates the revocation.
func (w *Warp[T]) RevokeLease(ctx context.Context, id string) {
	w.leases.Revoke(ctx, id)
}

// AttachKey associates a key with an existing lease.
func (w *Warp[T]) AttachKey(id, key string) {
	w.leases.Attach(id, key)
}

// Txn creates a new transaction associated with this Warp.
func (w *Warp[T]) Txn(ctx context.Context) *Txn[T] {
	return &Txn[T]{
		w:       w,
		ctx:     ctx,
		sets:    make(map[string]T),
		deletes: make(map[string]struct{}),
		cas:     make(map[string]T),
	}
}

// Set queues a key to be updated with the provided value.
func (t *Txn[T]) Set(key string, value T) {
	t.sets[key] = value
	delete(t.deletes, key)
	delete(t.cas, key)
}

// Delete queues a key for deletion.
func (t *Txn[T]) Delete(key string) {
	t.deletes[key] = struct{}{}
	delete(t.sets, key)
	delete(t.cas, key)
}

// CompareAndSwap queues a CAS operation for a key.
func (t *Txn[T]) CompareAndSwap(key string, old, new T) {
	t.sets[key] = new
	t.cas[key] = old
	delete(t.deletes, key)
}

// Commit applies all queued operations atomically.
func (t *Txn[T]) Commit() error {
	ctx, span := tracer.Start(t.ctx, "Warp.Txn.Commit")
	start := time.Now()
	defer func() {
		span.SetAttributes(attribute.Int64("warp.core.latency_ms", time.Since(start).Milliseconds()))
		span.End()
		if t.w.latencyHist != nil {
			t.w.latencyHist.Observe(time.Since(start).Seconds())
		}
	}()

	var batch adapter.Batch[T]
	if t.w.store != nil {
		if b, ok := t.w.store.(adapter.Batcher[T]); ok {
			var err error
			batch, err = b.Batch(ctx)
			if err != nil {
				return err
			}
		}
	}

	for key, val := range t.sets {
		shard := t.w.shard(key)
		shard.RLock()
		reg, ok := shard.regs[key]
		shard.RUnlock()
		if !ok {
			return ErrUnregistered
		}
		if reg.ttlStrategy != nil {
			reg.ttlStrategy.Record(key)
		}

		now := time.Now()
		newVal := merge.Value[T]{Data: val, Timestamp: now}

		metrics.SetCounter.Inc()
		if old, ok, err := t.w.cache.Get(ctx, key); err != nil {
			return err
		} else if ok {
			if expected, has := t.cas[key]; has {
				if !reflect.DeepEqual(old.Data, expected) {
					return ErrCASMismatch
				}
			}
			if merged, err := t.w.merges.Merge(key, old, newVal); err == nil {
				newVal = merged
			}
		} else if _, has := t.cas[key]; has {
			return ErrCASMismatch
		}

		ttl := reg.ttl
		if reg.ttlStrategy != nil {
			ttl = reg.ttlStrategy.TTL(key)
		}
		reg.mu.Lock()
		reg.currentTTL = ttl
		reg.lastAccess = now
		reg.mu.Unlock()
		if err := t.w.cache.Set(ctx, key, newVal, ttl); err != nil {
			return err
		}

		if batch != nil {
			if err := batch.Set(ctx, key, newVal.Data); err != nil {
				return err
			}
		} else if t.w.store != nil {
			if err := t.w.store.Set(ctx, key, newVal.Data); err != nil {
				return err
			}
		}

		if reg.mode != ModeStrongLocal && t.w.bus != nil {
			if err := t.w.bus.Publish(ctx, key); err != nil {
				return err
			}
		}
	}

	for key := range t.deletes {
		metrics.InvalidateCounter.Inc()
		shard := t.w.shard(key)
		shard.RLock()
		reg, ok := shard.regs[key]
		shard.RUnlock()
		if !ok {
			return ErrUnregistered
		}
		if err := t.w.cache.Invalidate(ctx, key); err != nil {
			return err
		}
		if t.w.evictionCounter != nil {
			t.w.evictionCounter.Inc()
		}
		if batch != nil {
			if err := batch.Delete(ctx, key); err != nil {
				return err
			}
		}

		if reg.mode != ModeStrongLocal && t.w.bus != nil {
			if err := t.w.bus.Publish(ctx, key); err != nil {
				return err
			}
		}
	}

	if batch != nil {
		if err := batch.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// validatorCache adapts the Warp cache to the Validator interface by operating on raw values.
type validatorCache[T any] struct {
	w *Warp[T]
}

func (vc validatorCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	if v, ok, err := vc.w.cache.Get(ctx, key); err != nil {
		var zero T
		return zero, false, err
	} else if ok {
		return v.Data, true, nil
	}
	var zero T
	return zero, false, nil
}

func (vc validatorCache[T]) Set(ctx context.Context, key string, value T, _ time.Duration) error {
	now := time.Now()
	mv := merge.Value[T]{Data: value, Timestamp: now}
	shard := vc.w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	var ttl time.Duration
	if ok {
		ttl = reg.ttl
		if reg.ttlStrategy != nil {
			reg.ttlStrategy.Record(key)
			ttl = reg.ttlStrategy.TTL(key)
		}
		reg.mu.Lock()
		reg.currentTTL = ttl
		reg.lastAccess = now
		reg.mu.Unlock()
	}
	return vc.w.cache.Set(ctx, key, mv, ttl)
}

func (vc validatorCache[T]) Invalidate(ctx context.Context, key string) error {
	return vc.w.cache.Invalidate(ctx, key)
}

// Validator returns a validator instance bound to this warp.
func (w *Warp[T]) Validator(mode validator.Mode, interval time.Duration) *validator.Validator[T] {
	return validator.New[T](validatorCache[T]{w: w}, w.store, mode, interval)
}
