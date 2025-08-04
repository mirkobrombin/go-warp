package validator

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
)

func TestValidatorAutoHeal(t *testing.T) {
	ctx := context.Background()
	c := cache.NewInMemory()
	s := adapter.NewInMemoryStore()
	s.Set(ctx, "k", "v1")
	c.Set(ctx, "k", "v0", 0)
	v := New(c, s, ModeAutoHeal, time.Millisecond)
	go v.Run(ctx)
	time.Sleep(5 * time.Millisecond)
	if val, ok := c.Get(ctx, "k"); !ok || val.(string) != "v1" {
		t.Fatalf("expected cache healed to v1, got %v", val)
	}
	if m := v.Metrics(); m == 0 {
		t.Fatalf("expected mismatch metrics > 0")
	}
}
