package cache

import (
	"context"
	"sync/atomic"
	"time"
)

// AdaptiveCache switches between LRU and LFU strategies based on access patterns.
//
// It monitors hit/miss ratios and selects the strategy with more hits.
type AdaptiveCache[T any] struct {
	lru Cache[T]
	lfu Cache[T]

	useLFU atomic.Bool
	hits   atomic.Uint64
	misses atomic.Uint64

	switchEvery uint64
}

// NewAdaptive creates a new AdaptiveCache.
//
// The cache starts with an LRU strategy and evaluates the hit/miss ratio
// every 100 operations, switching to LFU when misses dominate and back to
// LRU when hits dominate.
func NewAdaptive[T any]() *AdaptiveCache[T] {
	ac := &AdaptiveCache[T]{
		lru:         NewLRU[T](),
		lfu:         NewLFU[T](),
		switchEvery: 100,
	}
	ac.useLFU.Store(false)
	return ac
}

func (a *AdaptiveCache[T]) selectCache() Cache[T] {
	if a.useLFU.Load() {
		return a.lfu
	}
	return a.lru
}

func (a *AdaptiveCache[T]) adjust() {
	total := a.hits.Load() + a.misses.Load()
	if total < a.switchEvery {
		return
	}
	if a.misses.Load() > a.hits.Load() {
		a.useLFU.Store(true)
	} else {
		a.useLFU.Store(false)
	}
	a.hits.Store(0)
	a.misses.Store(0)
}

// Get implements Cache.Get.
func (a *AdaptiveCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	v, ok, err := a.selectCache().Get(ctx, key)
	if err != nil {
		var zero T
		return zero, false, err
	}
	if ok {
		a.hits.Add(1)
	} else {
		a.misses.Add(1)
	}
	a.adjust()
	return v, ok, nil
}

// Set stores the key in both underlying caches.
func (a *AdaptiveCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	if err := a.lru.Set(ctx, key, value, ttl); err != nil {
		return err
	}
	return a.lfu.Set(ctx, key, value, ttl)
}

// Invalidate removes the key from both caches.
func (a *AdaptiveCache[T]) Invalidate(ctx context.Context, key string) error {
	if err := a.lru.Invalidate(ctx, key); err != nil {
		return err
	}
	return a.lfu.Invalidate(ctx, key)
}

// Close releases resources held by the underlying caches.
func (a *AdaptiveCache[T]) Close() {
	if c, ok := a.lru.(*InMemoryCache[T]); ok {
		c.Close()
	}
	if c, ok := a.lfu.(*LFUCache[T]); ok {
		c.RistrettoCache.Close()
	}
}

var _ Cache[int] = (*AdaptiveCache[int])(nil)
