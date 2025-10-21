package versioned

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

func TestCacheSetGetHistory(t *testing.T) {
	base := cache.NewInMemory[merge.VersionedValue[int]]()
	c := New[int](base, 3)
	ctx := context.Background()
	v1 := merge.Value[int]{Data: 1, Timestamp: time.Now().Add(-3 * time.Minute)}
	v2 := merge.Value[int]{Data: 2, Timestamp: time.Now().Add(-2 * time.Minute)}
	v3 := merge.Value[int]{Data: 3, Timestamp: time.Now().Add(-1 * time.Minute)}

	if err := c.Set(ctx, "k", v1, time.Minute); err != nil {
		t.Fatalf("set v1: %v", err)
	}
	if err := c.Set(ctx, "k", v2, time.Minute); err != nil {
		t.Fatalf("set v2: %v", err)
	}
	if err := c.Set(ctx, "k", v3, time.Minute); err != nil {
		t.Fatalf("set v3: %v", err)
	}

	got, ok, err := c.Get(ctx, "k")
	if err != nil || !ok || got.Data != 3 {
		t.Fatalf("expected latest=3 got %v ok %v err %v", got.Data, ok, err)
	}

	at, ok, err := c.GetAt(ctx, "k", v2.Timestamp.Add(time.Millisecond))
	if err != nil || !ok || at.Data != 2 {
		t.Fatalf("expected value at v2, got %v ok %v err %v", at.Data, ok, err)
	}
}

func TestCacheHistoryLimit(t *testing.T) {
	base := cache.NewInMemory[merge.VersionedValue[int]]()
	c := New[int](base, 2)
	ctx := context.Background()
	now := time.Now()
	v1 := merge.Value[int]{Data: 1, Timestamp: now.Add(-3 * time.Minute)}
	v2 := merge.Value[int]{Data: 2, Timestamp: now.Add(-2 * time.Minute)}
	v3 := merge.Value[int]{Data: 3, Timestamp: now.Add(-1 * time.Minute)}

	_ = c.Set(ctx, "k", v1, time.Minute)
	_ = c.Set(ctx, "k", v2, time.Minute)
	_ = c.Set(ctx, "k", v3, time.Minute)

	// v1 should be evicted due to limit 2
	if _, ok, _ := c.GetAt(ctx, "k", v1.Timestamp); ok {
		t.Fatal("expected oldest version evicted")
	}
}

func TestCacheInvalidate(t *testing.T) {
	base := cache.NewInMemory[merge.VersionedValue[int]]()
	c := New[int](base, 1)
	ctx := context.Background()
	mv := merge.Value[int]{Data: 1, Timestamp: time.Now()}
	if err := c.Set(ctx, "k", mv, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := c.Invalidate(ctx, "k"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Fatal("expected key removed after invalidate")
	}
}

func TestCacheGlobalEviction(t *testing.T) {
	base := cache.NewInMemory[merge.VersionedValue[int]]()
	c := New[int](base, 1, WithMaxEntries[int](1))
	ctx := context.Background()
	mv := merge.Value[int]{Data: 1, Timestamp: time.Now()}
	if err := c.Set(ctx, "k1", mv, time.Minute); err != nil {
		t.Fatalf("set k1: %v", err)
	}
	if err := c.Set(ctx, "k2", mv, time.Minute); err != nil {
		t.Fatalf("set k2: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "k1"); ok {
		t.Fatal("expected k1 evicted due to max entries")
	}
	m := c.Metrics()
	if m.Evictions != 1 {
		t.Fatalf("expected 1 eviction got %d", m.Evictions)
	}
}

func TestCacheMetricsCounters(t *testing.T) {
	base := cache.NewInMemory[merge.VersionedValue[int]]()
	c := New[int](base, 1)
	ctx := context.Background()
	now := time.Now()

	if err := c.Set(ctx, "present", merge.Value[int]{Data: 1, Timestamp: now}, time.Minute); err != nil {
		t.Fatalf("set present: %v", err)
	}
	if _, ok, err := c.Get(ctx, "present"); err != nil || !ok {
		t.Fatalf("expected cache hit, got ok=%v err=%v", ok, err)
	}

	if _, ok, err := c.Get(ctx, "missing"); err != nil || ok {
		t.Fatalf("expected cache miss, got ok=%v err=%v", ok, err)
	}

	metrics := c.Metrics()
	if metrics.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", metrics.Hits)
	}
	if metrics.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", metrics.Misses)
	}
}
