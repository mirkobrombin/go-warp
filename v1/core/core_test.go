package core

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func TestWarpSetGet(t *testing.T) {
	ctx := context.Background()
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, syncbus.NewInMemoryBus(), merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, err := w.Get(ctx, "foo")
	if err != nil || v != "bar" {
		t.Fatalf("unexpected value: %v, err: %v", v, err)
	}
	if err := w.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := w.Get(ctx, "foo"); err == nil {
		t.Fatalf("expected error after invalidate")
	}
}

func TestWarpMerge(t *testing.T) {
	ctx := context.Background()
	w := New[int](cache.NewInMemory[merge.Value[int]](), adapter.NewInMemoryStore[int](), nil, merge.NewEngine[int]())
	w.Register("cnt", ModeStrongLocal, time.Minute)
	w.Merge("cnt", func(old, new int) (int, error) {
		return old + new, nil
	})
	if err := w.Set(ctx, "cnt", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := w.Set(ctx, "cnt", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, err := w.Get(ctx, "cnt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 3 {
		t.Fatalf("expected 3, got %v", v)
	}
}

func TestWarpFallbackAndWarmup(t *testing.T) {
	ctx := context.Background()
	store := adapter.NewInMemoryStore[string]()
	_ = store.Set(ctx, "foo", "bar")
	w := New[string](cache.NewInMemory[merge.Value[string]](), store, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	// fallback
	v, err := w.Get(ctx, "foo")
	if err != nil || v != "bar" {
		t.Fatalf("unexpected fallback value: %v err: %v", v, err)
	}
	// warmup
	if err := w.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w.Warmup(ctx)
	v, err = w.Get(ctx, "foo")
	if err != nil || v != "bar" {
		t.Fatalf("expected warmup to load value, got %v err %v", v, err)
	}
}
