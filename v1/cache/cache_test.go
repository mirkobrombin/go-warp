package cache

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryCache(t *testing.T) {
	ctx := context.Background()
	c := NewInMemory()
	if err := c.Set(ctx, "foo", "bar", time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if v, ok := c.Get(ctx, "foo"); !ok || v.(string) != "bar" {
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
	c := NewInMemory(WithSweepInterval(5 * time.Millisecond))
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
