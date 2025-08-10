package syncbus_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func newRedisBus(t *testing.T) (*syncbus.RedisBus, context.Context) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := syncbus.NewRedisBus(client)
	ctx := context.Background()
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})
	return bus, ctx
}

func TestRedisBusConcurrentInvalidations(t *testing.T) {
	bus, ctx := newRedisBus(t)
	store := adapter.NewInMemoryStore[int]()
	w1 := core.New[int](cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w2 := core.New[int](cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w1.Register("key", core.ModeStrongDistributed, time.Minute)
	w2.Register("key", core.ModeStrongDistributed, time.Minute)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = w1.Invalidate(ctx, "key")
	}()
	go func() {
		defer wg.Done()
		_ = w2.Invalidate(ctx, "key")
	}()
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected 1 published got %d", metrics.Published)
	}
}
