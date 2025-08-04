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
type Warp struct {
	cache  cache.Cache
	store  adapter.Store
	bus    syncbus.Bus
	merges *merge.Engine

	mu   sync.RWMutex
	regs map[string]registration
}

// New creates a new Warp instance.
func New(c cache.Cache, s adapter.Store, bus syncbus.Bus, m *merge.Engine) *Warp {
	if m == nil {
		m = merge.NewEngine()
	}
	return &Warp{
		cache:  c,
		store:  s,
		bus:    bus,
		merges: m,
		regs:   make(map[string]registration),
	}
}

// Register registers a key with a specific mode and TTL.
func (w *Warp) Register(key string, mode Mode, ttl time.Duration) {
	w.mu.Lock()
	w.regs[key] = registration{ttl: ttl, mode: mode}
	w.mu.Unlock()
}

// ErrNotFound is returned when a key does not exist in the cache.
var ErrNotFound = errors.New("warp: not found")

// Get retrieves a value from the cache.
func (w *Warp) Get(ctx context.Context, key string) (any, error) {
	if v, ok := w.cache.Get(ctx, key); ok {
		if mv, ok := v.(merge.Value); ok {
			return mv.Data, nil
		}
		return v, nil
	}
	if w.store != nil {
		w.mu.RLock()
		reg := w.regs[key]
		w.mu.RUnlock()
		if v, err := w.store.Get(ctx, key); err == nil && v != nil {
			mv := merge.Value{Data: v, Timestamp: time.Now()}
			w.cache.Set(ctx, key, mv, reg.ttl)
			return v, nil
		}
	}
	return nil, ErrNotFound
}

// Set stores a value in the cache, applying merge strategies and publishing if needed.
func (w *Warp) Set(ctx context.Context, key string, value any) {
	w.mu.RLock()
	reg := w.regs[key]
	w.mu.RUnlock()

	now := time.Now()
	newVal := merge.Value{Data: value, Timestamp: now}
	if old, ok := w.cache.Get(ctx, key); ok {
		if ov, ok := old.(merge.Value); ok {
			merged, err := w.merges.Merge(key, ov, newVal)
			if err == nil {
				newVal = merged
			}
		}
	}

	w.cache.Set(ctx, key, newVal, reg.ttl)
	if w.store != nil {
		_ = w.store.Set(ctx, key, newVal.Data)
	}

	if reg.mode != ModeStrongLocal && w.bus != nil {
		w.bus.Publish(ctx, key)
	}
}

// Invalidate removes a key and propagates the invalidation if required.
func (w *Warp) Invalidate(ctx context.Context, key string) {
	w.cache.Invalidate(ctx, key)
	w.mu.RLock()
	reg := w.regs[key]
	w.mu.RUnlock()
	if reg.mode != ModeStrongLocal && w.bus != nil {
		w.bus.Publish(ctx, key)
	}
}

// Merge registers a custom merge function for a key.
func (w *Warp) Merge(key string, fn merge.MergeFn) {
	w.merges.Register(key, fn)
}

// Warmup loads registered keys from the storage into the cache.
func (w *Warp) Warmup(ctx context.Context) {
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
		v, err := w.store.Get(ctx, k)
		if err != nil || v == nil {
			continue
		}
		w.mu.RLock()
		reg := w.regs[k]
		w.mu.RUnlock()
		mv := merge.Value{Data: v, Timestamp: time.Now()}
		w.cache.Set(ctx, k, mv, reg.ttl)
	}
}

// Validator returns a validator instance bound to this warp.
func (w *Warp) Validator(mode validator.Mode, interval time.Duration) *validator.Validator {
	return validator.New(w.cache, w.store, mode, interval)
}
