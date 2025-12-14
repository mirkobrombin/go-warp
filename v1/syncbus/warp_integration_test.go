package syncbus_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	busredis "github.com/mirkobrombin/go-warp/v1/syncbus/redis"
)

func newRedisBus(t *testing.T) (*busredis.RedisBus, context.Context) {
	t.Helper()
	addr := os.Getenv("WARP_TEST_REDIS_ADDR")
	forceReal := os.Getenv("WARP_TEST_FORCE_REAL") == "true"
	var client *redis.Client
	var mr *miniredis.Miniredis

	if forceReal && addr == "" {
		t.Fatal("WARP_TEST_FORCE_REAL is true but WARP_TEST_REDIS_ADDR is empty")
	}

	if addr != "" {
		t.Logf("TestRedisBus: using real Redis at %s", addr)
		client = redis.NewClient(&redis.Options{Addr: addr})
	} else {
		t.Log("TestRedisBus: using miniredis")
		var err error
		mr, err = miniredis.Run()
		if err != nil {
			t.Fatalf("miniredis run: %v", err)
		}
		client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	}

	bus := busredis.NewRedisBus(busredis.RedisBusOptions{Client: client})
	ctx := context.Background()
	t.Cleanup(func() {
		_ = bus.Close()
		if addr != "" {
			_ = client.FlushAll(context.Background()).Err()
			_ = client.Close()
		} else {
			_ = client.Close()
			mr.Close()
		}
	})
	return bus, ctx
}

func TestRedisBusConcurrentInvalidations(t *testing.T) {
	bus, ctx := newRedisBus(t)
	store := adapter.NewInMemoryStore[int]()
	w1 := core.New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w2 := core.New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w1.Register("key", core.ModeStrongDistributed, time.Minute)
	w2.Register("key", core.ModeStrongDistributed, time.Minute)

	sub, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = bus.Unsubscribe(context.Background(), "key", sub) })
	go func() {
		for range sub {
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := w1.Invalidate(ctx, "key"); err != nil {
			t.Errorf("w1 invalidate: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := w2.Invalidate(ctx, "key"); err != nil {
			t.Errorf("w2 invalidate: %v", err)
		}
	}()
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected 1 published got %d", metrics.Published)
	}
}
