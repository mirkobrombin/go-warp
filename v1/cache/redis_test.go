package cache

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

// newRedisCache returns a Redis-backed cache and context for testing.
// It also registers cleanup to flush data, close the client and stop the
// underlying miniredis server.
func newRedisCache[T any](t *testing.T) (*RedisCache[T], context.Context) {
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

	return NewRedis[T](client, nil), ctx
}

func TestRedisCacheGetSetInvalidate(t *testing.T) {
	c, ctx := newRedisCache[string](t)

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

func TestRedisCacheComplexStruct(t *testing.T) {
	type complex struct {
		Name string
		Age  int
		Tags []string
	}

	c, ctx := newRedisCache[complex](t)

	expected := complex{Name: "Alice", Age: 30, Tags: []string{"go", "redis"}}
	if err := c.Set(ctx, "user:1", expected, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, ok, err := c.Get(ctx, "user:1")
	if err != nil || !ok {
		t.Fatalf("expected value, got miss err %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %+v, got %+v", expected, got)
	}
}

func TestRedisCacheGetErrors(t *testing.T) {
	t.Run("client error", func(t *testing.T) {
		c, ctx := newRedisCache[string](t)
		_ = c.client.Close()
		if _, _, err := c.Get(ctx, "foo"); err == nil {
			t.Fatalf("expected error from closed client")
		}
	})

	t.Run("unmarshal error", func(t *testing.T) {
		c, ctx := newRedisCache[string](t)
		// store invalid JSON to trigger unmarshal error
		if err := c.client.Set(ctx, "foo", "{invalid", 0).Err(); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if _, _, err := c.Get(ctx, "foo"); err == nil {
			t.Fatalf("expected unmarshal error")
		}
	})
}
