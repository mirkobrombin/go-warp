package core

import (
	"context"
	"errors"
	"reflect"
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
)

var tracer = otel.Tracer("github.com/mirkobrombin/go-warp/v1/core")

// Mode represents the consistency mode for a key.
type Mode int

const (
	ModeStrongLocal Mode = iota
	ModeEventualDistributed
	ModeStrongDistributed
)

type registration struct {
	ttl         time.Duration
	ttlStrategy cache.TTLStrategy
	ttlOpts     cache.TTLOptions
	mode        Mode
	currentTTL  time.Duration
	lastAccess  time.Time
}

// Warp orchestrates the interaction between cache, merge engine and sync bus.
type Warp[T any] struct {
	cache  cache.Cache[merge.Value[T]]
	store  adapter.Store[T]
	bus    syncbus.Bus
	merges *merge.Engine[T]
	leases *LeaseManager[T]

	mu   sync.RWMutex
	regs map[string]*registration

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

// New creates a new Warp instance.
func New[T any](c cache.Cache[merge.Value[T]], s adapter.Store[T], bus syncbus.Bus, m *merge.Engine[T], opts ...Option[T]) *Warp[T] {
	if m == nil {
		m = merge.NewEngine[T]()
	}
	w := &Warp[T]{
		cache:  c,
		store:  s,
		bus:    bus,
		merges: m,
		regs:   make(map[string]*registration),
	}
	w.leases = newLeaseManager[T](w, bus)
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Register registers a key with a specific mode and TTL.
// Optional cache.TTLOption can enable sliding expiration or dynamic TTL
// adjustments. It returns false if the key was already registered.
func (w *Warp[T]) Register(key string, mode Mode, ttl time.Duration, opts ...cache.TTLOption) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.regs[key]; exists {
		return false
	}
	var to cache.TTLOptions
	for _, opt := range opts {
		opt(&to)
	}
	w.regs[key] = &registration{
		ttl:        ttl,
		ttlOpts:    to,
		mode:       mode,
		currentTTL: ttl,
	}
	return true
}

// RegisterDynamicTTL registers a key with a consistency mode and a dynamic TTL
// strategy. It returns false if the key was already registered.
func (w *Warp[T]) RegisterDynamicTTL(key string, mode Mode, strat cache.TTLStrategy, opts ...cache.TTLOption) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.regs[key]; exists {
		return false
	}
	var to cache.TTLOptions
	for _, opt := range opts {
		opt(&to)
	}
	w.regs[key] = &registration{ttlStrategy: strat, ttlOpts: to, mode: mode}
	return true
}

// Unregister removes a key registration.
func (w *Warp[T]) Unregister(key string) {
	w.mu.Lock()
	delete(w.regs, key)
	w.mu.Unlock()
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
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
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
					ttl = reg.ttlOpts.Adjust(ttl, reg.lastAccess, now)
				}
				reg.currentTTL = ttl
				reg.lastAccess = now
				_ = w.cache.Set(ctx, key, v, ttl)
			}
		} else if reg.ttlOpts.Sliding || reg.ttlOpts.FreqThreshold > 0 {
			reg.currentTTL = reg.ttlOpts.Adjust(reg.currentTTL, reg.lastAccess, now)
			reg.lastAccess = now
			_ = w.cache.Set(ctx, key, v, reg.currentTTL)
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
			reg.currentTTL = ttl
			reg.lastAccess = now
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
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
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
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
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
	reg.currentTTL = ttl
	reg.lastAccess = now
	if err := w.cache.Set(ctx, key, newVal, ttl); err != nil {
		return err
	}
	if w.store != nil {
		if err := w.store.Set(ctx, key, newVal.Data); err != nil {
			return err
		}
	}

	if reg.mode != ModeStrongLocal && w.bus != nil {
		if err := w.bus.Publish(ctx, key); err != nil {
			return err
		}
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
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
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
		if err := w.bus.Publish(ctx, key); err != nil {
			return err
		}
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
	w.mu.RLock()
	keys := make([]string, 0, len(w.regs))
	for k := range w.regs {
		keys = append(keys, k)
	}
	w.mu.RUnlock()
	for _, k := range keys {
		select {
		case <-ctx.Done():
			return
		default:
		}
		v, ok, err := w.store.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		w.mu.RLock()
		reg := w.regs[k]
		w.mu.RUnlock()
		now := time.Now()
		mv := merge.Value[T]{Data: v, Timestamp: now}
		ttl := reg.ttl
		if reg.ttlStrategy != nil {
			ttl = reg.ttlStrategy.TTL(k)
		}
		reg.currentTTL = ttl
		reg.lastAccess = now
		_ = w.cache.Set(ctx, k, mv, ttl)
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
		t.w.mu.RLock()
		reg, ok := t.w.regs[key]
		t.w.mu.RUnlock()
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
		reg.currentTTL = ttl
		reg.lastAccess = now
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
		t.w.mu.RLock()
		reg, ok := t.w.regs[key]
		t.w.mu.RUnlock()
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
	vc.w.mu.RLock()
	reg, ok := vc.w.regs[key]
	vc.w.mu.RUnlock()
	var ttl time.Duration
	if ok {
		ttl = reg.ttl
		if reg.ttlStrategy != nil {
			reg.ttlStrategy.Record(key)
			ttl = reg.ttlStrategy.TTL(key)
		}
		reg.currentTTL = ttl
		reg.lastAccess = now
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
