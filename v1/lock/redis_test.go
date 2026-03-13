package lock

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func newTestRedisLocker(t *testing.T) (*Redis, *miniredis.Miniredis, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	locker := NewRedis(client)
	return locker, mr, func() {
		_ = client.Close()
		mr.Close()
	}
}

// TestTryLock_basic verifies the acquire → contention → release → re-acquire
// cycle using TryLock.
func TestTryLock_basic(t *testing.T) {
	l, _, cleanup := newTestRedisLocker(t)
	defer cleanup()
	ctx := context.Background()

	ok, err := l.TryLock(ctx, "k", time.Second)
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v err=%v", ok, err)
	}

	// Lock is held — second attempt must fail.
	ok, err = l.TryLock(ctx, "k", time.Second)
	if err != nil || ok {
		t.Fatalf("second TryLock should fail while lock is held: ok=%v err=%v", ok, err)
	}

	if err := l.Release(ctx, "k"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// After release the key must be acquirable again.
	ok, err = l.TryLock(ctx, "k", time.Second)
	if err != nil || !ok {
		t.Fatalf("TryLock after Release: ok=%v err=%v", ok, err)
	}
}

// TestRelease_ownershipCheck verifies that a locker without the token for a key
// cannot release a lock held by another locker instance.
func TestRelease_ownershipCheck(t *testing.T) {
	l1, _, cleanup := newTestRedisLocker(t)
	defer cleanup()
	// l2 shares the same Redis but has no entry for "k".
	l2 := NewRedis(l1.client)
	ctx := context.Background()

	ok, err := l1.TryLock(ctx, "k", 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("l1.TryLock: ok=%v err=%v", ok, err)
	}

	// l2 has no token for "k"; Release must be a no-op.
	if err := l2.Release(ctx, "k"); err != nil {
		t.Fatalf("l2.Release: %v", err)
	}

	// l1 must still hold the lock.
	ok, err = l1.TryLock(ctx, "k", time.Second)
	if err != nil || ok {
		t.Fatalf("lock should still be held by l1 after l2.Release: ok=%v err=%v", ok, err)
	}

	if err := l1.Release(ctx, "k"); err != nil {
		t.Fatalf("l1.Release: %v", err)
	}
}

// TestAcquire_waits verifies that Acquire blocks while the lock is held and
// returns promptly after the holder calls Release.
func TestAcquire_waits(t *testing.T) {
	l, _, cleanup := newTestRedisLocker(t)
	defer cleanup()
	ctx := context.Background()

	// Goroutine A acquires the lock.
	ok, err := l.TryLock(ctx, "k", 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("TryLock: ok=%v err=%v", ok, err)
	}

	// Goroutine B tries to Acquire — must block.
	done := make(chan error, 1)
	go func() {
		done <- l.Acquire(ctx, "k", 5*time.Second)
	}()

	// Give B time to enter the wait loop.
	time.Sleep(20 * time.Millisecond)

	if err := l.Release(ctx, "k"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("B.Acquire returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("B did not acquire the lock within 500 ms after Release")
	}
}

// TestTTL_expiry verifies that a lock with a short TTL is released automatically
// by Redis, allowing a subsequent TryLock to succeed.
func TestTTL_expiry(t *testing.T) {
	l, mr, cleanup := newTestRedisLocker(t)
	defer cleanup()
	ctx := context.Background()

	ok, err := l.TryLock(ctx, "k", 100*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("TryLock: ok=%v err=%v", ok, err)
	}

	// Advance miniredis internal clock past the TTL without real sleeping.
	mr.FastForward(200 * time.Millisecond)

	// The Redis key has expired; a new TryLock must succeed.
	ok, err = l.TryLock(ctx, "k", time.Second)
	if err != nil || !ok {
		t.Fatalf("TryLock after TTL expiry: ok=%v err=%v", ok, err)
	}
}
