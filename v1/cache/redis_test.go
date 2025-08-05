package cache

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func TestRedisCacheComplexStruct(t *testing.T) {
	type complex struct {
		Name string
		Age  int
		Tags []string
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	c := NewRedis[complex](client, nil)
	ctx := context.Background()

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
