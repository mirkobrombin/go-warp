package lock

import (
	"context"
	"time"
)

// Locker defines basic distributed lock operations.
type Locker interface {
	// Acquire blocks until the lock for key is obtained or the context is
	// cancelled. If ttl is greater than zero, the lock is automatically
	// released after the duration.
	Acquire(ctx context.Context, key string, ttl time.Duration) error
	// TryLock attempts to obtain the lock without waiting. It returns true on
	// success.
	TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Release frees the lock for the given key.
	Release(ctx context.Context, key string) error
}
