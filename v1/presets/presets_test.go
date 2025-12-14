package presets

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mirkobrombin/go-warp/v1/core"
)

func TestNewInMemoryStandalone(t *testing.T) {
	w := NewInMemoryStandalone[string]()
	ctx := context.Background()

	w.Register("foo", core.ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	val, err := w.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "bar" {
		t.Fatalf("expected bar, got %s", val)
	}
}

func TestNewRedisEventual(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	defer mr.Close()

	w := NewRedisEventual[string](RedisOptions{Addr: mr.Addr()})
	ctx := context.Background()

	w.Register("foo", core.ModeEventualDistributed, time.Minute)
	if err := w.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	val, err := w.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "bar" {
		t.Fatalf("expected bar, got %s", val)
	}
}
