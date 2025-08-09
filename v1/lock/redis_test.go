package lock

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func newRedisLocker(t *testing.T) (*Redis, syncbus.Bus, context.Context, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := syncbus.NewInMemoryBus()
	locker := NewRedis(client, bus)
	ctx := context.Background()
	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}
	return locker, bus, ctx, cleanup
}

func TestRedisTryLockAcquireReleaseAndBus(t *testing.T) {
	l, bus, ctx, cleanup := newRedisLocker(t)
	defer cleanup()

	lockCh, err := bus.Subscribe(ctx, "lock:k")
	if err != nil {
		t.Fatalf("subscribe lock: %v", err)
	}
	unlockCh, err := bus.Subscribe(ctx, "unlock:k")
	if err != nil {
		t.Fatalf("subscribe unlock: %v", err)
	}

	if err := l.Acquire(ctx, "k", time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	select {
	case <-lockCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for lock publish")
	}
	if err := l.Release(ctx, "k"); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case <-unlockCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for unlock publish")
	}
	l.mu.Lock()
	if _, ok := l.tokens["k"]; ok {
		t.Fatal("token not cleaned up on release")
	}
	l.mu.Unlock()

	ok, err := l.TryLock(ctx, "k", time.Second)
	if err != nil || !ok {
		t.Fatalf("trylock: %v ok %v", err, ok)
	}
	if ok, err := l.TryLock(ctx, "k", time.Second); err != nil || ok {
		t.Fatalf("expected lock held, ok %v err %v", ok, err)
	}
	if err := l.Release(ctx, "k"); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestRedisAcquireTimeout(t *testing.T) {
	l1, bus, ctx, cleanup := newRedisLocker(t)
	defer cleanup()
	l2 := NewRedis(l1.client, bus)

	if ok, err := l1.TryLock(ctx, "k", 0); err != nil || !ok {
		t.Fatalf("initial trylock: %v ok %v", err, ok)
	}

	cctx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := l2.Acquire(cctx, "k", 0); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("acquire did not respect context timeout")
	}
}
