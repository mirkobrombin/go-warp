package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	natsserver "github.com/nats-io/nats-server/v2/test"
	nats "github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func TestWarpDistributedRedis(t *testing.T) {
	ctx := context.Background()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := syncbus.NewRedisBus(client)
	store := adapter.NewInMemoryStore[int]()

	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()
	w1 := New[int](cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg1))
	w2 := New[int](cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg2))

	w1.Register("counter", ModeStrongDistributed, time.Minute)
	w2.Register("counter", ModeStrongDistributed, time.Minute)
	mergeFn := func(old, new int) (int, error) { return old + new, nil }
	w1.Merge("counter", mergeFn)
	w2.Merge("counter", mergeFn)

	ch, err := bus.Subscribe(ctx, "counter")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Unsubscribe(context.Background(), "counter", ch)
		_ = client.Close()
		mr.Close()
	})
	go func() {
		for range ch {
			_ = w2.cache.Invalidate(ctx, "counter")
		}
	}()

	if err := w1.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set1: %v", err)
	}
	if err := w1.Set(ctx, "counter", 2); err != nil {
		t.Fatalf("set2: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	v, err := w2.Get(ctx, "counter")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 3 {
		t.Fatalf("expected 3 got %d", v)
	}

	before := bus.Metrics()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = w1.Invalidate(ctx, "counter")
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = w2.Invalidate(ctx, "counter")
	}()
	close(start)
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	after := bus.Metrics()
	if after.Published-before.Published != 1 {
		t.Fatalf("expected 1 publish, got %d", after.Published-before.Published)
	}

	ev := testutil.ToFloat64(w1.evictionCounter) + testutil.ToFloat64(w2.evictionCounter)
	if ev != 2 {
		t.Fatalf("expected 2 evictions got %v", ev)
	}
}

func TestWarpDistributedNATS(t *testing.T) {
	ctx := context.Background()
	s := natsserver.RunRandClientPortServer()
	conn, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	bus := syncbus.NewNATSBus(conn)
	store := adapter.NewInMemoryStore[int]()

	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()
	w1 := New[int](cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg1))
	w2 := New[int](cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg2))

	w1.Register("counter", ModeStrongDistributed, time.Minute)
	w2.Register("counter", ModeStrongDistributed, time.Minute)
	mergeFn := func(old, new int) (int, error) { return old + new, nil }
	w1.Merge("counter", mergeFn)
	w2.Merge("counter", mergeFn)

	ch, err := bus.Subscribe(ctx, "counter")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Unsubscribe(context.Background(), "counter", ch)
		conn.Close()
		s.Shutdown()
	})
	go func() {
		for range ch {
			_ = w2.cache.Invalidate(ctx, "counter")
		}
	}()

	if err := w1.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set1: %v", err)
	}
	if err := w1.Set(ctx, "counter", 2); err != nil {
		t.Fatalf("set2: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	v, err := w2.Get(ctx, "counter")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 3 {
		t.Fatalf("expected 3 got %d", v)
	}

	before := bus.Metrics()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = w1.Invalidate(ctx, "counter")
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = w2.Invalidate(ctx, "counter")
	}()
	close(start)
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	after := bus.Metrics()
	if after.Published-before.Published != 1 {
		t.Fatalf("expected 1 publish, got %d", after.Published-before.Published)
	}

	ev := testutil.ToFloat64(w1.evictionCounter) + testutil.ToFloat64(w2.evictionCounter)
	if ev != 2 {
		t.Fatalf("expected 2 evictions got %v", ev)
	}
}
