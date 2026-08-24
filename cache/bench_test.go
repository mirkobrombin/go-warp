package cache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

// benchmarkSet measures Set performance for a cache.
func benchmarkSet(b *testing.B, c Cache[string]) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Set(ctx, strconv.Itoa(i), "val", time.Minute); err != nil {
			b.Fatalf("set failed: %v", err)
		}
	}
}

// benchmarkGet measures Get performance for a cache.
func benchmarkGet(b *testing.B, c Cache[string]) {
	ctx := context.Background()
	if err := c.Set(ctx, "key", "val", time.Minute); err != nil {
		b.Fatalf("setup failed: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, err := c.Get(ctx, "key"); err != nil || !ok {
			b.Fatalf("get failed: %v ok=%v", err, ok)
		}
	}
}

func BenchmarkInMemoryCacheSet(b *testing.B) {
	c := NewInMemory[string]()
	defer c.Close()
	benchmarkSet(b, c)
}

func BenchmarkInMemoryCacheGet(b *testing.B) {
	c := NewInMemory[string]()
	defer c.Close()
	benchmarkGet(b, c)
}

func BenchmarkRistrettoCacheSet(b *testing.B) {
	c := NewRistretto[string]()
	defer c.Close()
	benchmarkSet(b, c)
}

func BenchmarkRistrettoCacheGet(b *testing.B) {
	c := NewRistretto[string]()
	defer c.Close()
	benchmarkGet(b, c)
}

// benchRedisCache returns a RedisCache backed by an in-memory Redis server.
func benchRedisCache() (*RedisCache[string], func()) {
	mr, _ := miniredis.Run()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}
	return NewRedis[string](client, nil), cleanup
}

func BenchmarkRedisCacheSet(b *testing.B) {
	c, cleanup := benchRedisCache()
	defer cleanup()
	benchmarkSet(b, c)
}

func BenchmarkRedisCacheGet(b *testing.B) {
	c, cleanup := benchRedisCache()
	defer cleanup()
	benchmarkGet(b, c)
}
