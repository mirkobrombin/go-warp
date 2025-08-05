package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	"github.com/mirkobrombin/go-warp/v1/validator"
)

// Mode represents the consistency mode for a key.
type Mode int

const (
	ModeStrongLocal Mode = iota
	ModeEventualDistributed
	ModeStrongDistributed
)

type registration struct {
	ttl  time.Duration
	mode Mode
}

// Warp orchestrates the interaction between cache, merge engine and sync bus.
type Warp[T any] struct {
	cache  cache.Cache[merge.Value[T]]
	store  adapter.Store[T]
	bus    syncbus.Bus
	merges *merge.Engine[T]

	mu   sync.RWMutex
	regs map[string]registration
}

// New creates a new Warp instance.
func New[T any](c cache.Cache[merge.Value[T]], s adapter.Store[T], bus syncbus.Bus, m *merge.Engine[T]) *Warp[T] {
	if m == nil {
		m = merge.NewEngine[T]()
	}
	return &Warp[T]{
		cache:  c,
		store:  s,
		bus:    bus,
		merges: m,
		regs:   make(map[string]registration),
	}
}

// Register registers a key with a specific mode and TTL.
// It returns false if the key was already registered.
func (w *Warp[T]) Register(key string, mode Mode, ttl time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.regs[key]; exists {
		return false
	}
	w.regs[key] = registration{ttl: ttl, mode: mode}
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

// Get retrieves a value from the cache.
func (w *Warp[T]) Get(ctx context.Context, key string) (T, error) {
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
	if !ok {
		var zero T
		return zero, ErrUnregistered
	}
	if v, ok := w.cache.Get(ctx, key); ok {
		return v.Data, nil
	}
	if w.store != nil {
		v, ok, err := w.store.Get(ctx, key)
		if err != nil {
			var zero T
			return zero, err
		}
		if ok {
			mv := merge.Value[T]{Data: v, Timestamp: time.Now()}
			_ = w.cache.Set(ctx, key, mv, reg.ttl)
			return mv.Data, nil
		}
	}
	var zero T
	return zero, ErrNotFound
}

// Set stores a value in the cache, applying merge strategies and publishing if needed.
// It returns an error if persisting the value to the underlying store fails.
func (w *Warp[T]) Set(ctx context.Context, key string, value T) error {
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
	if !ok {
		return ErrUnregistered
	}

	now := time.Now()
	newVal := merge.Value[T]{Data: value, Timestamp: now}
	if old, ok := w.cache.Get(ctx, key); ok {
		merged, err := w.merges.Merge(key, old, newVal)
		if err == nil {
			newVal = merged
		}
	}

	if err := w.cache.Set(ctx, key, newVal, reg.ttl); err != nil {
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
	w.mu.RLock()
	reg, ok := w.regs[key]
	w.mu.RUnlock()
	if !ok {
		return ErrUnregistered
	}
	if err := w.cache.Invalidate(ctx, key); err != nil {
		return err
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
		v, ok, err := w.store.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		w.mu.RLock()
		reg := w.regs[k]
		w.mu.RUnlock()
		mv := merge.Value[T]{Data: v, Timestamp: time.Now()}
		_ = w.cache.Set(ctx, k, mv, reg.ttl)
	}
}

// validatorCache adapts the Warp cache to the Validator interface by operating on raw values.
type validatorCache[T any] struct {
	c cache.Cache[merge.Value[T]]
}

func (vc validatorCache[T]) Get(ctx context.Context, key string) (T, bool) {
	if v, ok := vc.c.Get(ctx, key); ok {
		return v.Data, true
	}
	var zero T
	return zero, false
}

func (vc validatorCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	mv := merge.Value[T]{Data: value, Timestamp: time.Now()}
	return vc.c.Set(ctx, key, mv, ttl)
}

func (vc validatorCache[T]) Invalidate(ctx context.Context, key string) error {
	return vc.c.Invalidate(ctx, key)
}

// Validator returns a validator instance bound to this warp.
func (w *Warp[T]) Validator(mode validator.Mode, interval time.Duration) *validator.Validator[T] {
	return validator.New[T](validatorCache[T]{w.cache}, w.store, mode, interval)
}
