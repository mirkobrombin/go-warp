package cache

import (
	"context"
	"time"

	"github.com/dgraph-io/ristretto"
)

// RistrettoCache implements Cache using dgraph-io/ristretto.
type RistrettoCache[T any] struct {
	c *ristretto.Cache
}

// RistrettoOption configures the underlying ristretto cache.
type RistrettoOption func(*ristretto.Config)

// WithRistretto applies a custom ristretto configuration.
//
// If cfg is nil, defaults are used.
func WithRistretto(cfg *ristretto.Config) RistrettoOption {
	return func(c *ristretto.Config) {
		if cfg == nil {
			return
		}
		*c = *cfg
	}
}

// NewRistretto returns a Cache backed by ristretto.
//
// Default configuration aims for a generous in-memory cache.
func NewRistretto[T any](opts ...RistrettoOption) *RistrettoCache[T] {
	cfg := &ristretto.Config{
		NumCounters: 1e4,     // number of keys to track frequency of (10k).
		MaxCost:     1 << 20, // maximum cost of cache (1MB by default).
		BufferItems: 64,      // number of keys per Get buffer.
	}
	for _, opt := range opts {
		opt(cfg)
	}
	rc, err := ristretto.NewCache(cfg)
	if err != nil {
		panic(err)
	}
	return &RistrettoCache[T]{c: rc}
}

// Get implements Cache.Get.
func (r *RistrettoCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	default:
	}
	v, ok := r.c.Get(key)
	if !ok {
		var zero T
		return zero, false, nil
	}
	select {
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	default:
	}
	val, _ := v.(T)
	return val, true, nil
}

// Set implements Cache.Set.
func (r *RistrettoCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	cost := EstimateSize(value)
	r.c.SetWithTTL(key, value, cost, ttl)
	r.c.Wait()
	return nil
}

// Invalidate implements Cache.Invalidate.
func (r *RistrettoCache[T]) Invalidate(ctx context.Context, key string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	r.c.Del(key)
	r.c.Wait()
	return nil
}

// Close releases resources held by the cache.
func (r *RistrettoCache[T]) Close() {
	r.c.Close()
}
