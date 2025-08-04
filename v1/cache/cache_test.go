package cache

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryCache(t *testing.T) {
	ctx := context.Background()
	c := NewInMemory()
	c.Set(ctx, "foo", "bar", time.Millisecond)

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
