package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newRistrettoCache returns a Ristretto-backed cache for testing.
func newRistrettoCache[T any](t *testing.T) (*RistrettoCache[T], context.Context) {
	t.Helper()
	c := NewRistretto[T]()
	ctx := context.Background()
	t.Cleanup(func() { c.Close() })
	return c, ctx
}

func TestRistrettoCacheGetSetInvalidate(t *testing.T) {
	c, ctx := newRistrettoCache[string](t)

	if err := c.Set(ctx, "foo", "bar", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok, err := c.Get(ctx, "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("Get: expected bar, got %v err %v", v, err)
	}
	if err := c.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok, err := c.Get(ctx, "foo"); ok || err != nil {
		t.Fatalf("expected miss after invalidate")
	}
}

func TestRistrettoCacheExpiration(t *testing.T) {
	c, ctx := newRistrettoCache[string](t)

	if err := c.Set(ctx, "foo", "bar", 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok, err := c.Get(ctx, "foo"); ok || err != nil {
		t.Fatalf("expected key to expire")
	}
}

func TestRistrettoCacheContext(t *testing.T) {
	c, _ := newRistrettoCache[string](t)
	defer c.Close()

	ctxSet, cancelSet := context.WithCancel(context.Background())
	cancelSet()
	if err := c.Set(ctxSet, "a", "b", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if _, ok, err := c.Get(context.Background(), "a"); ok || err != nil {
		t.Fatalf("item should not be stored when context is canceled")
	}

	if err := c.Set(context.Background(), "foo", "bar", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctxGet, cancelGet := context.WithCancel(context.Background())
	cancelGet()
	if v, ok, err := c.Get(ctxGet, "foo"); !errors.Is(err, context.Canceled) || ok || v != "" {
		t.Fatalf("expected canceled context to prevent retrieval")
	}

	ctxInv, cancelInv := context.WithCancel(context.Background())
	cancelInv()
	if err := c.Invalidate(ctxInv, "foo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if v, ok, err := c.Get(context.Background(), "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("item should remain after canceled invalidate")
	}
}
