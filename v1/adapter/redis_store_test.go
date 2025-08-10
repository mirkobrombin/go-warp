package adapter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
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

// newRedisStoreWithServer returns a Redis-backed store along with the
// underlying miniredis server and client for tests that need to manipulate
// the server state.
func newRedisStoreWithServer[T any](t *testing.T) (*adapter.RedisStore[T], context.Context, *miniredis.Miniredis, *redis.Client) {
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
		if mr != nil {
			mr.Close()
		}
	})
	return adapter.NewRedisStore[T](client), ctx, mr, client
}

func TestRedisStoreSetMarshalError(t *testing.T) {
	s, ctx := newRedisStore[chan int](t)
	ch := make(chan int)
	if err := s.Set(ctx, "foo", ch); err == nil {
		t.Fatalf("expected marshal error")
	}
}

func TestRedisStoreGetUnmarshalError(t *testing.T) {
	s, ctx, _, client := newRedisStoreWithServer[string](t)
	if err := client.Set(ctx, "foo", "invalid", 0).Err(); err != nil {
		t.Fatalf("client.Set: %v", err)
	}
	if _, _, err := s.Get(ctx, "foo"); err == nil {
		t.Fatalf("expected unmarshal error")
	}
}

func TestRedisStoreKeysScanError(t *testing.T) {
	s, ctx, mr, _ := newRedisStoreWithServer[string](t)
	mr.Close()
	mr = nil
	if _, err := s.Keys(ctx); err == nil {
		t.Fatalf("expected scan error")
	}
}

func TestRedisStoreBatchCommitError(t *testing.T) {
	s, ctx, mr, _ := newRedisStoreWithServer[string](t)
	b, err := s.Batch(ctx)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if err := b.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Batch Set: %v", err)
	}
	mr.Close()
	mr = nil
	if err := b.Commit(ctx); err == nil {
		t.Fatalf("expected commit error")
	}
}

func TestRedisStoreSentinelErrors(t *testing.T) {
	t.Run("connection closed", func(t *testing.T) {
		s, ctx, _, client := newRedisStoreWithServer[string](t)
		_ = s.Set(ctx, "foo", "bar")
		_ = client.Close()
		if _, _, err := s.Get(ctx, "foo"); !errors.Is(err, warperrors.ErrConnectionClosed) {
			t.Fatalf("expected connection closed, got %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		s, ctx := newRedisStore[string](t)
		tCtx, cancel := context.WithTimeout(ctx, time.Nanosecond)
		defer cancel()
		time.Sleep(time.Millisecond)
		if _, _, err := s.Get(tCtx, "foo"); !errors.Is(err, warperrors.ErrTimeout) {
			t.Fatalf("expected timeout, got %v", err)
		}
	})
}
