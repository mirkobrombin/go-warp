package cache

import (
	"context"
	"log/slog"
	"time"
)

// ResilientCache wraps a Cache implementation and suppresses errors,
// logging them instead of returning them. This ensures that cache failures
// (e.g. Redis down) do not propagate to the application, treating them
// as cache misses or successful (but skipped) writes.
type ResilientCache[T any] struct {
	inner Cache[T]
}

// NewResilient creates a new ResilientCache wrapper.
func NewResilient[T any](inner Cache[T]) *ResilientCache[T] {
	return &ResilientCache[T]{inner: inner}
}

// Get implements Cache.Get.
// If the inner cache fails, it logs the error and returns a cache miss.
func (r *ResilientCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	val, ok, err := r.inner.Get(ctx, key)
	if err != nil {
		slog.Warn("warp: cache get failed (resiliency active)", "key", key, "error", err)
		var zero T
		return zero, false, nil // Treat error as miss
	}
	return val, ok, nil
}

// Set implements Cache.Set.
// If the inner cache fails, it logs the error and returns nil (success).
func (r *ResilientCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	if err := r.inner.Set(ctx, key, value, ttl); err != nil {
		slog.Warn("warp: cache set failed (resiliency active)", "key", key, "error", err)
		return nil // Suppress error
	}
	return nil
}

// Invalidate implements Cache.Invalidate.
// If the inner cache fails, it logs the error and returns nil (success).
func (r *ResilientCache[T]) Invalidate(ctx context.Context, key string) error {
	if err := r.inner.Invalidate(ctx, key); err != nil {
		slog.Warn("warp: cache invalidate failed (resiliency active)", "key", key, "error", err)
		return nil // Suppress error
	}
	return nil
}
