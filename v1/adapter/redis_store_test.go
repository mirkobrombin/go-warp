package adapter_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

// newRedisStore returns a Redis-backed store and context for testing.
// It also registers cleanup to flush data, close the client and stop the
// underlying miniredis server.
func newRedisStore[T any](t *testing.T) (*adapter.RedisStore[T], context.Context) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	t.Cleanup(func() {
		_ = client.FlushDB(ctx).Err()
		_ = client.Close()
		mr.Close()
	})
	return adapter.NewRedisStore[T](client), ctx
}

func TestRedisStoreGetSetKeys(t *testing.T) {
	s, ctx := newRedisStore[string](t)
	if err := s.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok, err := s.Get(ctx, "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("Get: expected bar, got %v err %v", v, err)
	}
	keys, err := s.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "foo" {
		t.Fatalf("Keys: expected [foo], got %v", keys)
	}
}

func TestRedisStorePersistenceAndWarmup(t *testing.T) {
	s, ctx := newRedisStore[string](t)

	// warp1 writes a value which should persist in Redis
	c1 := cache.NewInMemory[merge.Value[string]]()
	w1 := core.New[string](c1, s, nil, merge.NewEngine[string]())
	w1.Register("foo", core.ModeStrongLocal, time.Minute)
	if err := w1.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// warp2 uses a fresh cache but same store; warmup should load persisted value
	c2 := cache.NewInMemory[merge.Value[string]]()
	w2 := core.New[string](c2, s, nil, merge.NewEngine[string]())
	w2.Register("foo", core.ModeStrongLocal, time.Minute)
	w2.Warmup(ctx)

	if v, err := w2.Get(ctx, "foo"); err != nil || v != "bar" {
		t.Fatalf("Warmup/Get: expected bar, got %v err %v", v, err)
	}
}
