package versioned

import (
	"context"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

// Cache wraps a cache.Cache of VersionedValue to keep multiple versions per key.
// It exposes the cache.Cache interface for merge.Value while maintaining
// up to `limit` historical versions per key.
type Cache[T any] struct {
	base  cache.Cache[merge.VersionedValue[T]]
	limit int
}

// New creates a new versioned cache with the provided base cache and history limit.
func New[T any](base cache.Cache[merge.VersionedValue[T]], limit int) *Cache[T] {
	return &Cache[T]{base: base, limit: limit}
}

// Get returns the most recent value for a key.
func (c *Cache[T]) Get(ctx context.Context, key string) (merge.Value[T], bool, error) {
	vv, ok, err := c.base.Get(ctx, key)
	if err != nil || !ok {
		var zero merge.Value[T]
		return zero, ok, err
	}
	v, vok := vv.Latest()
	return v, vok, nil
}

// GetAt returns the value associated with the key at the specified time.
func (c *Cache[T]) GetAt(ctx context.Context, key string, at time.Time) (merge.Value[T], bool, error) {
	vv, ok, err := c.base.Get(ctx, key)
	if err != nil || !ok {
		var zero merge.Value[T]
		return zero, ok, err
	}
	v, vok := vv.At(at)
	return v, vok, nil
}

// Set stores a new value for the key and records the history.
func (c *Cache[T]) Set(ctx context.Context, key string, val merge.Value[T], ttl time.Duration) error {
	vv, ok, err := c.base.Get(ctx, key)
	if err != nil {
		return err
	}
	if !ok {
		vv = merge.NewVersionedValue(val, c.limit)
	} else {
		vv.Add(val)
	}
	return c.base.Set(ctx, key, vv, ttl)
}

// Invalidate removes all versions of the key.
func (c *Cache[T]) Invalidate(ctx context.Context, key string) error {
	return c.base.Invalidate(ctx, key)
}

// ensure Cache implements cache.Cache for merge.Value
var _ cache.Cache[merge.Value[struct{}]] = (*Cache[struct{}])(nil)
