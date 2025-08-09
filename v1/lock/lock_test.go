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
