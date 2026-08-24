package cache

import (
	"context"
	"testing"
	"time"
)

func TestAdaptiveCacheInvalidate(t *testing.T) {
	ctx := context.Background()
	ac := NewAdaptive[string]()
	defer ac.Close()

	if err := ac.Set(ctx, "foo", "bar", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ac.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if _, ok, _ := ac.lru.Get(ctx, "foo"); ok {
		t.Fatalf("expected lru cache to remove key")
	}
	if _, ok, _ := ac.lfu.Get(ctx, "foo"); ok {
		t.Fatalf("expected lfu cache to remove key")
	}
}

func TestAdaptiveCacheClose(t *testing.T) {
	ctx := context.Background()
	ac := NewAdaptive[string]()
	if err := ac.Set(ctx, "foo", "bar", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac.Close()
	if c, ok := ac.lru.(*InMemoryCache[string]); ok {
		c.mu.RLock()
		size := len(c.items)
		c.mu.RUnlock()
		if size != 0 {
			t.Fatalf("expected lru cache to be empty after close")
		}
	}
	if _, ok, _ := ac.lfu.Get(ctx, "foo"); ok {
		t.Fatalf("expected lfu cache to be empty after close")
	}
}
