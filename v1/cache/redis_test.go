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

	if v, ok := c.Get(ctx, "foo"); !ok || v != "bar" {
		t.Fatalf("Get: expected bar, got %v", v)
	}

	if err := c.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	if _, ok := c.Get(ctx, "foo"); ok {
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

	got, ok := c.Get(ctx, "user:1")
	if !ok {
		t.Fatalf("expected value, got miss")
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %+v, got %+v", expected, got)
	}
}
