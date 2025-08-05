package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryCache(t *testing.T) {
	ctx := context.Background()
	c := NewInMemory[string]()
	defer c.Close()
	if err := c.Set(ctx, "foo", "bar", time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if v, ok := c.Get(ctx, "foo"); !ok || v != "bar" {
		t.Fatalf("expected bar, got %v", v)
	}

	time.Sleep(2 * time.Millisecond)
	if _, ok := c.Get(ctx, "foo"); ok {
		t.Fatalf("expected key to expire")
	}

	m := c.Metrics()
	if m.Hits != 1 || m.Misses != 1 {
		t.Fatalf("unexpected metrics: %+v", m)
	}
}

func TestInMemoryCacheSweeper(t *testing.T) {
	ctx := context.Background()
	c := NewInMemory[string](WithSweepInterval[string](5 * time.Millisecond))
	defer c.Close()
	if err := c.Set(ctx, "foo", "bar", 5*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	c.mu.RLock()
	_, ok := c.items["foo"]
	c.mu.RUnlock()
	if ok {
		t.Fatalf("expected key to be swept")
	}
}

func TestInMemoryCacheContext(t *testing.T) {
	c := NewInMemory[string]()
	defer c.Close()

	// Set with canceled context should fail and not store the item.
	ctxSet, cancelSet := context.WithCancel(context.Background())
	cancelSet()
	if err := c.Set(ctxSet, "a", "b", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if _, ok := c.Get(context.Background(), "a"); ok {
		t.Fatalf("item should not be stored when context is canceled")
	}

	// Prepare an item for further context tests.
	if err := c.Set(context.Background(), "foo", "bar", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Get with canceled context should not retrieve the item.
	ctxGet, cancelGet := context.WithCancel(context.Background())
	cancelGet()
	if v, ok := c.Get(ctxGet, "foo"); ok || v != "" {
		t.Fatalf("expected canceled context to prevent retrieval")
	}

	// Invalidate with canceled context should fail and keep the item.
	ctxInv, cancelInv := context.WithCancel(context.Background())
	cancelInv()
	if err := c.Invalidate(ctxInv, "foo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if v, ok := c.Get(context.Background(), "foo"); !ok || v != "bar" {
		t.Fatalf("item should remain after canceled invalidate")
	}
}
