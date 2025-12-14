package redis

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"

	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
)

func newRedisBus(t *testing.T) (*RedisBus, context.Context) {
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

	bus := NewRedisBus(RedisBusOptions{Client: client})
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

func TestRedisBusPublishSubscribeFlowAndMetrics(t *testing.T) {
	bus, ctx := newRedisBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(ctx, "key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected published 1 got %d", metrics.Published)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected delivered 1 got %d", metrics.Delivered)
	}
}

func TestRedisBusContextBasedUnsubscribe(t *testing.T) {
	bus, _ := newRedisBus(t)
	subCtx, cancel := context.WithCancel(context.Background())
	ch, err := bus.Subscribe(subCtx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for unsubscribe")
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, ok := bus.subs["key"]; ok {
		t.Fatal("subscription still present after context cancel")
	}
}

func TestRedisBusDeduplicatePendingKeys(t *testing.T) {
	bus, ctx := newRedisBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	bus.mu.Lock()
	bus.pending["key"] = struct{}{}
	bus.mu.Unlock()
	if err := bus.Publish(ctx, "key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ch:
		t.Fatal("unexpected publish when key pending")
	default:
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
	if metrics.Delivered != 0 {
		t.Fatalf("expected delivered 0 got %d", metrics.Delivered)
	}
}

func TestRedisBusReconnectAfterClose(t *testing.T) {
	bus, ctx := newRedisBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = bus.client.Close()
	if err := bus.Publish(ctx, "key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected published 1 got %d", metrics.Published)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected delivered 1 got %d", metrics.Delivered)
	}
}

func TestRedisBusReconnectLoop(t *testing.T) {
	bus, ctx := newRedisBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = bus.client.Close()
	done := make(chan error, 1)
	go func() { done <- bus.Publish(ctx, "key") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publish timeout")
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
}

func TestRedisBusIdempotentAfterReconnect(t *testing.T) {
	bus, ctx := newRedisBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	id := uuid.NewString()
	if err := bus.client.Publish(ctx, "key", id).Err(); err != nil {
		t.Fatalf("direct publish: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
	_ = bus.client.Close()
	if err := bus.reconnect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if err := bus.client.Publish(ctx, "key", id).Err(); err != nil {
		t.Fatalf("dup publish: %v", err)
	}
	select {
	case <-ch:
		t.Fatal("duplicate delivered")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRedisBusTimeout(t *testing.T) {
	bus, ctx := newRedisBus(t)
	tCtx, cancel := context.WithTimeout(ctx, time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if err := bus.Publish(tCtx, "key"); !errors.Is(err, warperrors.ErrTimeout) {
		t.Fatalf("expected timeout error, got %v", err)
	}
}
