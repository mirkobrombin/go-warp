package syncbus

import (
	"context"
	"testing"
	"time"
)

func TestPublishSubscribeFlowAndMetrics(t *testing.T) {
	bus := NewInMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), "key"); err != nil {
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

func TestContextBasedUnsubscribe(t *testing.T) {
	bus := NewInMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := bus.Subscribe(ctx, "key")
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

func TestDeduplicatePendingKeys(t *testing.T) {
	bus := NewInMemoryBus()
	ctx := context.Background()
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	bus.mu.Lock()
	bus.pending["key"] = struct{}{}
	bus.mu.Unlock()

	if err := bus.Publish(context.Background(), "key"); err != nil {
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

func TestPublishContextCanceled(t *testing.T) {
	bus := NewInMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bus.Publish(ctx, "key"); err == nil {
		t.Fatal("expected publish error due to canceled context")
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
	if metrics.Delivered != 0 {
		t.Fatalf("expected delivered 0 got %d", metrics.Delivered)
	}
}

func TestSubscribeContextCanceled(t *testing.T) {
	bus := NewInMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bus.Subscribe(ctx, "key"); err == nil {
		t.Fatal("expected subscribe error due to canceled context")
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, ok := bus.subs["key"]; ok {
		t.Fatal("subscription should not be added when context is canceled")
	}
}

func TestUnsubscribeContextCanceled(t *testing.T) {
	bus := NewInMemoryBus()
	ch, err := bus.Subscribe(context.Background(), "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bus.Unsubscribe(ctx, "key", ch); err == nil {
		t.Fatal("expected unsubscribe error due to canceled context")
	}
	bus.mu.Lock()
	if _, ok := bus.subs["key"]; !ok {
		bus.mu.Unlock()
		t.Fatal("subscription should remain when unsubscribe context is canceled")
	}
	bus.mu.Unlock()
	if err := bus.Unsubscribe(context.Background(), "key", ch); err != nil {
		t.Fatalf("cleanup unsubscribe: %v", err)
	}
}
