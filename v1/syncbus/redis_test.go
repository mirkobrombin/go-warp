package syncbus

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func newRedisBus(t *testing.T) (*RedisBus, context.Context) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := NewRedisBus(client)
	ctx := context.Background()
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
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

func TestRedisBusPublishError(t *testing.T) {
	bus, ctx := newRedisBus(t)
	_ = bus.client.Close()
	if err := bus.Publish(ctx, "key"); err == nil {
		t.Fatal("expected publish error")
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
}
