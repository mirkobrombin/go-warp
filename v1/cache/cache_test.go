package cache

import (
	"context"
	"errors"
	"fmt"
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

	if v, ok, err := c.Get(ctx, "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("expected bar, got %v err %v", v, err)
	}

	time.Sleep(2 * time.Millisecond)
	if _, ok, err := c.Get(ctx, "foo"); ok || err != nil {
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
	if _, ok, err := c.Get(context.Background(), "a"); ok || err != nil {
		t.Fatalf("item should not be stored when context is canceled")
	}

	// Prepare an item for further context tests.
	if err := c.Set(context.Background(), "foo", "bar", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Get with canceled context should not retrieve the item.
	ctxGet, cancelGet := context.WithCancel(context.Background())
	cancelGet()
	if v, ok, err := c.Get(ctxGet, "foo"); !errors.Is(err, context.Canceled) || ok || v != "" {
		t.Fatalf("expected canceled context to prevent retrieval")
	}

	// Invalidate with canceled context should fail and keep the item.
	ctxInv, cancelInv := context.WithCancel(context.Background())
	cancelInv()
	if err := c.Invalidate(ctxInv, "foo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if v, ok, err := c.Get(context.Background(), "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("item should remain after canceled invalidate")
	}
}

func TestInMemoryCacheEviction(t *testing.T) {
	ctx := context.Background()
	c := NewInMemory[string](WithMaxEntries[string](2))
	defer c.Close()

	if err := c.Set(ctx, "a", "1", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Set(ctx, "b", "2", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Access "a" so that "b" becomes the least recently used.
	if _, ok, err := c.Get(ctx, "a"); err != nil || !ok {
		t.Fatalf("expected to retrieve a: %v", err)
	}

	if err := c.Set(ctx, "c", "3", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok, _ := c.Get(ctx, "b"); ok {
		t.Fatalf("expected b to be evicted")
	}
	if _, ok, _ := c.Get(ctx, "a"); !ok {
		t.Fatalf("expected a to remain in cache")
	}
	if _, ok, _ := c.Get(ctx, "c"); !ok {
		t.Fatalf("expected c to be present")
	}
}

func TestNewStrategies(t *testing.T) {
	c := New[int]()
	if _, ok := c.(*InMemoryCache[int]); !ok {
		t.Fatalf("expected LRU cache by default")
	}
	c = New[int](WithStrategy[int](LFUStrategy))
	if _, ok := c.(*LFUCache[int]); !ok {
		t.Fatalf("expected LFU cache")
	}
}

func TestAdaptiveSwitch(t *testing.T) {
	ctx := context.Background()
	c := New[int](WithStrategy[int](AdaptiveStrategy))
	ac, ok := c.(*AdaptiveCache[int])
	if !ok {
		t.Fatalf("expected AdaptiveCache")
	}
	defer ac.Close()
	for i := 0; i < 150; i++ {
		key := fmt.Sprintf("k%d", i)
		c.Get(ctx, key)
	}
	if !ac.useLFU.Load() {
		t.Fatalf("expected adaptive cache to switch to LFU")
	}
}
