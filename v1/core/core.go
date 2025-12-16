package core

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
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
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
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
	quorum      int
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

	group singleflight.Group

	hitCounter      prometheus.Counter
	missCounter     prometheus.Counter
	evictionCounter prometheus.Counter
	latencyHist     prometheus.Histogram
	traceEnabled    bool
	resilient       bool
	publishTimeout  time.Duration
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

// WithCacheResiliency enables the L2 Fail-Safe pattern.
// If the cache (L1 or L2) returns an error (e.g. connection down),
// Warp will suppress the error and proceed as if it were a cache miss (for Get)
// or a successful operation (for Set/Invalidate), ensuring application stability.
func WithCacheResiliency[T any]() Option[T] {
	return func(w *Warp[T]) {
		w.resilient = true
	}
}

// WithPublishTimeout sets a timeout for background syncbus publish operations
// in ModeEventualDistributed. This prevents goroutine leaks if the bus is slow or down.
func WithPublishTimeout[T any](d time.Duration) Option[T] {
	return func(w *Warp[T]) {
		w.publishTimeout = d
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

	if w.resilient {
		w.cache = cache.NewResilient(w.cache)
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
		quorum:     1,
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
	shard.regs[key] = &registration{ttlStrategy: strat, ttlOpts: to, mode: mode, quorum: 1}
	return true
}

// Unregister removes a key registration.
func (w *Warp[T]) Unregister(key string) {
	shard := w.shard(key)
	shard.Lock()
	delete(shard.regs, key)
	shard.Unlock()
}

// SetQuorum configures the quorum size for strong distributed operations on a key.
// Values less than 1 are coerced to 1. It returns false if the key is not registered.
func (w *Warp[T]) SetQuorum(key string, replicas int) bool {
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		return false
	}
	if replicas < 1 {
		replicas = 1
	}
	reg.mu.Lock()
	reg.quorum = replicas
	reg.mu.Unlock()
	return true
}

// SetQuorumAware configures the quorum requirements for strong distributed operations,
// specifying a minimum number of distinct Availability Zones (AZs) that must acknowledge the update.
// If minZones > 1, the bus must support topology-aware publishing.
func (w *Warp[T]) SetQuorumAware(key string, replicas int, minZones int) bool {
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		return false
	}
	if replicas < 1 {
		replicas = 1
	}
	if minZones < 1 {
		minZones = 1
	}
	reg.mu.Lock()
	reg.quorum = replicas
	reg.mu.Unlock()
	return true
}

func (r *registration) quorumSize() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.quorum < 1 {
		return 1
	}
	return r.quorum
}

// ErrNotFound is returned when a key does not exist in the cache.
var ErrNotFound = errors.New("warp: not found")

// ErrUnregistered is returned when a key is not registered.
var ErrUnregistered = errors.New("warp: key not registered")

// ErrCASMismatch is returned when the expected value differs from the current one.
var ErrCASMismatch = errors.New("warp: cas mismatch")

// ErrBusRequired is returned when a distributed mode is used without a sync bus.
var ErrBusRequired = errors.New("warp: sync bus required for distributed mode")

// Get retrieves a value from the cache.
func (w *Warp[T]) Get(ctx context.Context, key string) (T, error) {
	if w.hitCounter != nil {
		metrics.GetCounter.Inc()
	}

	var span trace.Span
	var start time.Time
	if w.traceEnabled {
		ctx, span = tracer.Start(ctx, "Warp.Get")
		defer span.End()
		start = time.Now()
	} else if w.latencyHist != nil {
		start = time.Now()
	}

	if w.traceEnabled || w.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if w.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.core.latency_ms", latency.Milliseconds()))
			}
			if w.latencyHist != nil {
				w.latencyHist.Observe(latency.Seconds())
			}
		}()
	}
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
		slog.Error("cache get failed", "key", key, "error", err)
		var zero T
		return zero, err
	} else if ok {
		now := time.Now()

		reg.mu.Lock()
		currentTTL := reg.currentTTL
		reg.mu.Unlock()

		// Fail-Safe: Check if the value is logically expired (stale)
		isStale := false
		if reg.ttlOpts.FailSafeGracePeriod > 0 {
			if now.After(v.Timestamp.Add(currentTTL)) {
				isStale = true
			}
		}

		if isStale {
			if w.store != nil {
				// Try to refresh from source
				vInterface, errS, _ := w.group.Do(key, func() (interface{}, error) {
					// Apply SoftTimeout if configured
					fetchCtx := ctx
					if reg.ttlOpts.SoftTimeout > 0 {
						var cancel context.CancelFunc
						fetchCtx, cancel = context.WithTimeout(ctx, reg.ttlOpts.SoftTimeout)
						defer cancel()
					}

					val, ok, err := w.store.Get(fetchCtx, key)
					if err != nil {
						return nil, err
					}
					if !ok {
						return nil, ErrNotFound
					}
					now := time.Now()
					return merge.Value[T]{Data: val, Timestamp: now}, nil
				})

				if errS != nil {
					// Source failed: return stale data if error is not NotFound
					// This catches both store errors and SoftTimeout (DeadlineExceeded)
					if !errors.Is(errS, ErrNotFound) {
						// Log differently for timeout vs error
						if errors.Is(errS, context.DeadlineExceeded) {
							slog.Warn("warp: soft timeout activated", "key", key)
						} else {
							slog.Warn("warp: fail-safe activated", "key", key, "error", errS)
						}

						if w.hitCounter != nil {
							w.hitCounter.Inc()
						}
						return v.Data, nil
					}
					// If ErrNotFound, fall through to return error (do not return stale)
					return v.Data, ErrNotFound
				} else {
					// Source success: update cache and return new
					mv := vInterface.(merge.Value[T])

					ttl := currentTTL
					if reg.ttlStrategy != nil {
						ttl = reg.ttlStrategy.TTL(key)
					}

					reg.mu.Lock()
					reg.currentTTL = ttl
					reg.lastAccess = mv.Timestamp
					reg.mu.Unlock()

					effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
					_ = w.cache.Set(ctx, key, mv, effectiveTTL)

					if w.hitCounter != nil {
						w.hitCounter.Inc()
					}
					if w.traceEnabled {
						span.SetAttributes(attribute.String("warp.core.result", "hit_refreshed"))
					}
					return mv.Data, nil
				}
			}
		}

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
				effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
				_ = w.cache.Set(ctx, key, v, effectiveTTL)
			}
		} else if reg.ttlOpts.Sliding || reg.ttlOpts.FreqThreshold > 0 {
			reg.mu.Lock()
			ttl := reg.ttlOpts.Adjust(reg.currentTTL, reg.lastAccess, now)
			reg.currentTTL = ttl
			reg.lastAccess = now
			reg.mu.Unlock()
			effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
			_ = w.cache.Set(ctx, key, v, effectiveTTL)
		}
		// Eager Refresh: If the item is fresh but close to expiration, trigger a background refresh
		if reg.ttlOpts.EagerRefreshThreshold > 0 && w.store != nil {
			reg.mu.Lock()
			currentTTL := reg.currentTTL // Use the current logical TTL
			reg.mu.Unlock()

			logicalExpiry := v.Timestamp.Add(currentTTL)
			remainingTTL := logicalExpiry.Sub(now) // 'now' is from the outer scope, correct

			// Ensure currentTTL is positive to avoid division by zero or negative ratio
			if currentTTL > 0 && float64(remainingTTL)/float64(currentTTL) <= reg.ttlOpts.EagerRefreshThreshold {
				slog.Debug("warp: eager refresh triggered", "key", key)
				go func() {
					// Use context.WithoutCancel to ensure background refresh can complete independently
					refreshCtx := context.WithoutCancel(context.Background())

					// The singleflight group handles deduplication of concurrent refresh attempts
					vInterface, errS, _ := w.group.Do(key, func() (interface{}, error) {
						// SoftTimeout applies to eager refresh as well
						fetchCtx := refreshCtx
						if reg.ttlOpts.SoftTimeout > 0 {
							var cancel context.CancelFunc
							fetchCtx, cancel = context.WithTimeout(refreshCtx, reg.ttlOpts.SoftTimeout)
							defer cancel()
						}

						val, ok, err := w.store.Get(fetchCtx, key)
						if err != nil {
							slog.Error("warp: eager refresh failed", "key", key, "error", err)
							return nil, err
						}
						if !ok {
							slog.Debug("warp: eager refresh found no data in store", "key", key)
							return nil, ErrNotFound
						}
						nowRefresh := time.Now() // Use current time for refresh
						return merge.Value[T]{Data: val, Timestamp: nowRefresh}, nil
					})

					if errS != nil {
						if !errors.Is(errS, ErrNotFound) && !errors.Is(errS, context.DeadlineExceeded) {
							slog.Error("warp: eager refresh completed with error", "key", key, "error", errS)
						}
						return // Nothing to update if fetch failed
					}

					mv := vInterface.(merge.Value[T])

					// Re-evaluate TTL in case strategy changed
					refreshTTL := currentTTL // Start with currentTTL
					if reg.ttlStrategy != nil {
						refreshTTL = reg.ttlStrategy.TTL(key) // Apply dynamic TTL if any
					}

					reg.mu.Lock()
					reg.currentTTL = refreshTTL
					reg.lastAccess = mv.Timestamp
					reg.mu.Unlock()

					effectiveRefreshTTL := refreshTTL + reg.ttlOpts.FailSafeGracePeriod
					if err := w.cache.Set(refreshCtx, key, mv, effectiveRefreshTTL); err != nil {
						slog.Error("warp: eager refresh failed to set cache", "key", key, "error", err)
					}
					slog.Debug("warp: eager refresh completed successfully", "key", key)
				}()
			}
		}

		if w.hitCounter != nil {
			w.hitCounter.Inc()
		}
		if w.traceEnabled {
			span.SetAttributes(attribute.String("warp.core.result", "hit"))
		}
		return v.Data, nil
	} else {
		if w.missCounter != nil {
			w.missCounter.Inc()
		}
		if w.traceEnabled {
			span.SetAttributes(attribute.String("warp.core.result", "miss"))
		}
	}
	if w.store != nil {
		vInterface, errS, _ := w.group.Do(key, func() (interface{}, error) {
			val, ok, err := w.store.Get(ctx, key)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ErrNotFound // Indicate not found with a specific error
			}
			now := time.Now()
			return merge.Value[T]{Data: val, Timestamp: now}, nil
		})
		if errS != nil {
			var zero T
			if errors.Is(errS, ErrNotFound) {
				return zero, ErrNotFound
			}
			return zero, errS
		}
		mv := vInterface.(merge.Value[T])

		ttl := reg.ttl
		if reg.ttlStrategy != nil {
			ttl = reg.ttlStrategy.TTL(key)
		}
		reg.mu.Lock()
		reg.currentTTL = ttl
		reg.lastAccess = mv.Timestamp
		reg.mu.Unlock()

		effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
		_ = w.cache.Set(ctx, key, mv, effectiveTTL)
		return mv.Data, nil
	}
	var zero T
	return zero, ErrNotFound
}

// GetOrSet retrieves a value from the cache. If the key is not found, it executes the provided loader function,
// stores the result in the cache, and returns it. Concurrent calls for the same key are deduplicated (singleflight).
// The key must be registered beforehand to determine TTL and consistency mode.
func (w *Warp[T]) GetOrSet(ctx context.Context, key string, loader func(context.Context) (T, error)) (T, error) {
	if w.hitCounter != nil {
		metrics.GetCounter.Inc()
	}

	var span trace.Span
	var start time.Time
	if w.traceEnabled {
		ctx, span = tracer.Start(ctx, "Warp.GetOrSet")
		defer span.End()
		start = time.Now()
	} else if w.latencyHist != nil {
		start = time.Now()
	}

	if w.traceEnabled || w.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if w.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.core.latency_ms", latency.Milliseconds()))
			}
			if w.latencyHist != nil {
				w.latencyHist.Observe(latency.Seconds())
			}
		}()
	}

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
		// If resiliency is on, we suppress error and treat as miss, otherwise error.
		slog.Error("cache get failed (GetOrSet)", "key", key, "error", err)
		var zero T
		return zero, err
	} else if ok {
		now := time.Now()
		reg.mu.Lock()
		currentTTL := reg.currentTTL
		reg.mu.Unlock()

		isStale := false
		if reg.ttlOpts.FailSafeGracePeriod > 0 {
			if now.After(v.Timestamp.Add(currentTTL)) {
				isStale = true
			}
		}

		if isStale {
			// Fail-Safe Logic for GetOrSet
			// We try to run the loader. If it fails, we return stale data.
			vInterface, errS, _ := w.group.Do(key, func() (interface{}, error) {
				val, err := loader(ctx)
				if err != nil {
					return nil, err
				}
				now := time.Now()
				return merge.Value[T]{Data: val, Timestamp: now}, nil
			})

			if errS != nil {
				// Loader failed.
				slog.Warn("warp: fail-safe activated (GetOrSet)", "key", key, "error", errS)
				if w.hitCounter != nil {
					w.hitCounter.Inc()
				}
				return v.Data, nil
			}

			// Loader success
			mv := vInterface.(merge.Value[T])

			ttl := currentTTL
			if reg.ttlStrategy != nil {
				ttl = reg.ttlStrategy.TTL(key)
			}
			reg.mu.Lock()
			reg.currentTTL = ttl
			reg.lastAccess = mv.Timestamp
			reg.mu.Unlock()

			effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
			_ = w.cache.Set(ctx, key, mv, effectiveTTL)

			if w.hitCounter != nil {
				w.hitCounter.Inc()
			}
			return mv.Data, nil
		}

		// Sliding Expiration Logic
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
				effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
				_ = w.cache.Set(ctx, key, v, effectiveTTL)
			}
		} else if reg.ttlOpts.Sliding || reg.ttlOpts.FreqThreshold > 0 {
			reg.mu.Lock()
			ttl := reg.ttlOpts.Adjust(reg.currentTTL, reg.lastAccess, now)
			reg.currentTTL = ttl
			reg.lastAccess = now
			reg.mu.Unlock()
			effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
			_ = w.cache.Set(ctx, key, v, effectiveTTL)
		}

		if w.hitCounter != nil {
			w.hitCounter.Inc()
		}
		if w.traceEnabled {
			span.SetAttributes(attribute.String("warp.core.result", "hit"))
		}
		return v.Data, nil
	}

	if w.missCounter != nil {
		w.missCounter.Inc()
	}
	if w.traceEnabled {
		span.SetAttributes(attribute.String("warp.core.result", "miss"))
	}

	vInterface, errS, _ := w.group.Do(key, func() (interface{}, error) {
		val, err := loader(ctx)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		return merge.Value[T]{Data: val, Timestamp: now}, nil
	})

	if errS != nil {
		var zero T
		return zero, errS
	}

	mv := vInterface.(merge.Value[T])

	ttl := reg.ttl
	if reg.ttlStrategy != nil {
		ttl = reg.ttlStrategy.TTL(key)
	}
	reg.mu.Lock()
	reg.currentTTL = ttl
	reg.lastAccess = mv.Timestamp
	reg.mu.Unlock()

	if err := w.Set(ctx, key, mv.Data); err != nil {
		slog.Error("warp: GetOrSet failed to set value", "key", key, "error", err)
	}

	return mv.Data, nil
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
		if w.traceEnabled {
			span.SetAttributes(attribute.String("warp.core.result", "hit"))
		}
		return v.Data, nil
	}
	if w.missCounter != nil {
		w.missCounter.Inc()
	}
	if w.traceEnabled {
		span.SetAttributes(attribute.String("warp.core.result", "miss"))
	}
	var zero T
	return zero, ErrNotFound
}

// Set stores a value in the cache, applying merge strategies and publishing if needed.
// It returns an error if persisting the value to the underlying store fails.
func (w *Warp[T]) Set(ctx context.Context, key string, value T) error {
	if w.hitCounter != nil {
		metrics.SetCounter.Inc()
	}

	var span trace.Span
	var start time.Time
	if w.traceEnabled {
		ctx, span = tracer.Start(ctx, "Warp.Set")
		defer span.End()
		start = time.Now()
	} else if w.latencyHist != nil {
		start = time.Now()
	}

	if w.traceEnabled || w.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if w.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.core.latency_ms", latency.Milliseconds()))
			}
			if w.latencyHist != nil {
				w.latencyHist.Observe(latency.Seconds())
			}
		}()
	}
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
	// FailSafe: Extend physical TTL in cache to allow for stale retrieval
	effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod

	applyLocal := func() error {
		if err := w.cache.Set(ctx, key, newVal, effectiveTTL); err != nil {
			return err
		}
		if w.store != nil {
			if err := w.store.Set(ctx, key, newVal.Data); err != nil {
				return err
			}
		}
		reg.mu.Lock()
		reg.currentTTL = ttl
		reg.lastAccess = now
		reg.mu.Unlock()
		return nil
	}

	switch reg.mode {
	case ModeEventualDistributed:
		if err := applyLocal(); err != nil {
			return err
		}
		if w.bus != nil {
			if !w.bus.IsHealthy() {
				slog.Warn("syncbus unhealthy, degrading to local set", "key", key)
				return nil
			}
			go func() {
				pubCtx := context.Background()
				if w.publishTimeout > 0 {
					var cancel context.CancelFunc
					pubCtx, cancel = context.WithTimeout(pubCtx, w.publishTimeout)
					defer cancel()
				} else {
					pubCtx = context.WithoutCancel(ctx)
				}

				if err := w.bus.Publish(pubCtx, key); err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						slog.Warn("warp: syncbus publish timed out", "key", key)
					}
					select {
					case w.publishErrCh <- err:
					default:
					}
				}
			}()
		}
	case ModeStrongDistributed:
		if w.bus == nil {
			return ErrBusRequired
		}
		if !w.bus.IsHealthy() {
			slog.Warn("syncbus unhealthy, degrading to local set", "key", key)
			if err := applyLocal(); err != nil {
				return err
			}
			return nil
		}
		quorum := reg.quorumSize()
		if err := w.bus.PublishAndAwait(ctx, key, quorum); err != nil {
			return err
		}
		if err := applyLocal(); err != nil {
			return err
		}
	default:
		if err := applyLocal(); err != nil {
			return err
		}
	}

	return nil
}

// Invalidate removes a key and propagates the invalidation if required.
// It returns an error if removing the key from the cache fails.
func (w *Warp[T]) Invalidate(ctx context.Context, key string) error {
	if w.hitCounter != nil {
		metrics.InvalidateCounter.Inc()
	}

	var span trace.Span
	var start time.Time
	if w.traceEnabled {
		ctx, span = tracer.Start(ctx, "Warp.Invalidate")
		defer span.End()
		start = time.Now()
	} else if w.latencyHist != nil {
		start = time.Now()
	}

	if w.traceEnabled || w.latencyHist != nil {
		defer func() {
			latency := time.Since(start)
			if w.traceEnabled {
				span.SetAttributes(attribute.Int64("warp.core.latency_ms", latency.Milliseconds()))
			}
			if w.latencyHist != nil {
				w.latencyHist.Observe(latency.Seconds())
			}
		}()
	}
	shard := w.shard(key)
	shard.RLock()
	reg, ok := shard.regs[key]
	shard.RUnlock()
	if !ok {
		return ErrUnregistered
	}
	applyLocal := func() error {
		if err := w.cache.Invalidate(ctx, key); err != nil {
			return err
		}
		if w.evictionCounter != nil {
			w.evictionCounter.Inc()
		}
		return nil
	}

	switch reg.mode {
	case ModeEventualDistributed:
		if err := applyLocal(); err != nil {
			return err
		}
		if w.bus != nil {
			if !w.bus.IsHealthy() {
				slog.Warn("syncbus unhealthy, degrading to local invalidate", "key", key)
				return nil
			}
			go func() {
				pubCtx := context.Background()
				if w.publishTimeout > 0 {
					var cancel context.CancelFunc
					pubCtx, cancel = context.WithTimeout(pubCtx, w.publishTimeout)
					defer cancel()
				} else {
					pubCtx = context.WithoutCancel(ctx)
				}

				if err := w.bus.Publish(pubCtx, key); err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						slog.Warn("warp: syncbus publish timed out", "key", key)
					}
					select {
					case w.publishErrCh <- err:
					default:
					}
				}
			}()
		}
	case ModeStrongDistributed:
		if w.bus == nil {
			return ErrBusRequired
		}
		if !w.bus.IsHealthy() {
			slog.Warn("syncbus unhealthy, degrading to local invalidate", "key", key)
			if err := applyLocal(); err != nil {
				return err
			}
			return nil
		}
		quorum := reg.quorumSize()
		if err := w.bus.PublishAndAwait(ctx, key, quorum); err != nil {
			return err
		}
		if err := applyLocal(); err != nil {
			return err
		}
	default:
		if err := applyLocal(); err != nil {
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

		effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod
		_ = w.cache.Set(ctx, r.key, mv, effectiveTTL)
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

	var strongSetOps []func() error
	var strongDeleteOps []func() error

	for key, val := range t.sets {
		keyCopy := key
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

		effectiveTTL := ttl + reg.ttlOpts.FailSafeGracePeriod

		newValCopy := newVal
		ttlCopy := effectiveTTL
		nowCopy := now
		regCopy := reg

		applyLocal := func() error {
			if err := t.w.cache.Set(ctx, keyCopy, newValCopy, ttlCopy); err != nil {
				return err
			}
			if batch != nil {
				if err := batch.Set(ctx, keyCopy, newValCopy.Data); err != nil {
					return err
				}
			} else if t.w.store != nil {
				if err := t.w.store.Set(ctx, keyCopy, newValCopy.Data); err != nil {
					return err
				}
			}
			regCopy.mu.Lock()
			regCopy.currentTTL = ttlCopy
			regCopy.lastAccess = nowCopy
			regCopy.mu.Unlock()
			return nil
		}

		switch reg.mode {
		case ModeEventualDistributed:
			if err := applyLocal(); err != nil {
				return err
			}
			if t.w.bus != nil {
				if err := t.w.bus.Publish(ctx, keyCopy); err != nil {
					return err
				}
			}
		case ModeStrongDistributed:
			if t.w.bus == nil {
				return ErrBusRequired
			}
			quorum := reg.quorumSize()
			if err := t.w.bus.PublishAndAwait(ctx, keyCopy, quorum); err != nil {
				return err
			}
			strongSetOps = append(strongSetOps, applyLocal)
		default:
			if err := applyLocal(); err != nil {
				return err
			}
		}
	}

	for key := range t.deletes {
		keyCopy := key
		metrics.InvalidateCounter.Inc()
		shard := t.w.shard(key)
		shard.RLock()
		reg, ok := shard.regs[key]
		shard.RUnlock()
		if !ok {
			return ErrUnregistered
		}
		applyLocal := func() error {
			if err := t.w.cache.Invalidate(ctx, keyCopy); err != nil {
				return err
			}
			if t.w.evictionCounter != nil {
				t.w.evictionCounter.Inc()
			}
			if batch != nil {
				if err := batch.Delete(ctx, keyCopy); err != nil {
					return err
				}
			}
			return nil
		}

		switch reg.mode {
		case ModeEventualDistributed:
			if err := applyLocal(); err != nil {
				return err
			}
			if t.w.bus != nil {
				if err := t.w.bus.Publish(ctx, keyCopy); err != nil {
					return err
				}
			}
		case ModeStrongDistributed:
			if t.w.bus == nil {
				return ErrBusRequired
			}
			quorum := reg.quorumSize()
			if err := t.w.bus.PublishAndAwait(ctx, keyCopy, quorum); err != nil {
				return err
			}
			strongDeleteOps = append(strongDeleteOps, applyLocal)
		default:
			if err := applyLocal(); err != nil {
				return err
			}
		}
	}

	for _, op := range strongSetOps {
		if err := op(); err != nil {
			return err
		}
	}

	for _, op := range strongDeleteOps {
		if err := op(); err != nil {
			return err
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

// WithTracing enables OpenTelemetry tracing for core operations.
func WithTracing[T any]() Option[T] {
	return func(w *Warp[T]) {
		w.traceEnabled = true
	}
}
