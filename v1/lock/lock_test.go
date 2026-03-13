package lock

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryTryLockAcquireRelease(t *testing.T) {
	l := NewInMemory(nil)
	ctx := context.Background()
	ok, err := l.TryLock(ctx, "k", time.Second)
	if err != nil || !ok {
		t.Fatalf("trylock: %v ok %v", err, ok)
	}
	if ok, err := l.TryLock(ctx, "k", time.Second); err != nil || ok {
		t.Fatalf("expected lock held, got ok %v err %v", ok, err)
	}
	if err := l.Release(ctx, "k"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, err := l.TryLock(ctx, "k", time.Second); err != nil || !ok {
		t.Fatalf("expected lock re-acquired, ok %v err %v", ok, err)
	}
}

// TestInMemoryAcquireNoLostWakeup verifies that Acquire never misses a Release
// notification even when Release fires in the narrow window between a failed
// TryLock and the Subscribe call (the lost-wakeup race).
//
// The test runs many iterations: in each one goroutine A holds the lock while
// goroutine B calls Acquire. A releases immediately, racing with B's subscribe
// step. With the fix in place, B is guaranteed to receive the notification and
// must unblock well within the 100 ms deadline.
func TestInMemoryAcquireNoLostWakeup(t *testing.T) {
	const iterations = 200
	const deadline = 100 * time.Millisecond

	for i := 0; i < iterations; i++ {
		l := NewInMemory(nil)
		ctx := context.Background()

		ok, err := l.TryLock(ctx, "k", 0)
		if err != nil || !ok {
			t.Fatalf("iter %d: initial TryLock: ok=%v err=%v", i, ok, err)
		}

		done := make(chan error, 1)
		go func() {
			done <- l.Acquire(ctx, "k", 0)
		}()

		// Release immediately, deliberately racing with B's subscribe step.
		if err := l.Release(ctx, "k"); err != nil {
			t.Fatalf("iter %d: release: %v", i, err)
		}

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("iter %d: Acquire returned unexpected error: %v", i, err)
			}
		case <-time.After(deadline):
			t.Fatalf("iter %d: Acquire did not complete within %v — lost wakeup", i, deadline)
		}
	}
}

func TestInMemoryAcquireTimeout(t *testing.T) {
	l := NewInMemory(nil)
	ctx := context.Background()
	_, _ = l.TryLock(ctx, "k", 0)

	cctx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := l.Acquire(cctx, "k", 0)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("acquire did not respect context timeout")
	}
}

func TestInMemoryLockTTLExpires(t *testing.T) {
	l := NewInMemory(nil)
	ctx := context.Background()
	if ok, err := l.TryLock(ctx, "k", 10*time.Millisecond); err != nil || !ok {
		t.Fatalf("trylock: %v ok %v", err, ok)
	}
	time.Sleep(20 * time.Millisecond)
	if ok, err := l.TryLock(ctx, "k", 0); err != nil || !ok {
		t.Fatalf("lock should expire, ok %v err %v", ok, err)
	}
}
