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
	w := New(cache.NewInMemory(), nil, syncbus.NewInMemoryBus(), merge.NewEngine())
	w.Register("foo", ModeStrongLocal, time.Minute)
	w.Set(ctx, "foo", "bar")
	v, err := w.Get(ctx, "foo")
	if err != nil || v.(string) != "bar" {
		t.Fatalf("unexpected value: %v, err: %v", v, err)
	}
	w.Invalidate(ctx, "foo")
	if _, err := w.Get(ctx, "foo"); err == nil {
		t.Fatalf("expected error after invalidate")
	}
}

func TestWarpMerge(t *testing.T) {
	ctx := context.Background()
	w := New(cache.NewInMemory(), adapter.NewInMemoryStore(), nil, merge.NewEngine())
	w.Register("cnt", ModeStrongLocal, time.Minute)
	w.Merge("cnt", func(old, new any) (any, error) {
		return old.(int) + new.(int), nil
	})
	w.Set(ctx, "cnt", 1)
	w.Set(ctx, "cnt", 2)
	v, err := w.Get(ctx, "cnt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.(int) != 3 {
		t.Fatalf("expected 3, got %v", v)
	}
}

func TestWarpFallbackAndWarmup(t *testing.T) {
	ctx := context.Background()
	store := adapter.NewInMemoryStore()
	store.Set(ctx, "foo", "bar")
	w := New(cache.NewInMemory(), store, nil, merge.NewEngine())
	w.Register("foo", ModeStrongLocal, time.Minute)
	// fallback
	v, err := w.Get(ctx, "foo")
	if err != nil || v.(string) != "bar" {
		t.Fatalf("unexpected fallback value: %v err: %v", v, err)
	}
	// warmup
	w.Invalidate(ctx, "foo")
	w.Warmup(ctx)
	v, err = w.Get(ctx, "foo")
	if err != nil || v.(string) != "bar" {
		t.Fatalf("expected warmup to load value, got %v err %v", v, err)
	}
}
