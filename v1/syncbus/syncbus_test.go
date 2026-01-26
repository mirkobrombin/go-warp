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

func TestPublishAndAwaitQuorumSatisfied(t *testing.T) {
	bus := NewInMemoryBus()
	ctx := context.Background()
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	done := make(chan struct{})
	go func() {
		if err := bus.PublishAndAwait(ctx, "key", 1); err != nil {
			t.Errorf("publish and await: %v", err)
		}
		close(done)
	}()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for quorum publish")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish and await did not return")
	}

	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected published 1 got %d", metrics.Published)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected delivered 1 got %d", metrics.Delivered)
	}
}

func TestPublishAndAwaitQuorumNotSatisfied(t *testing.T) {
	bus := NewInMemoryBus()
	ctx := context.Background()
	if err := bus.PublishAndAwait(ctx, "key", 2); err != ErrQuorumNotSatisfied {
		t.Fatalf("expected quorum error, got %v", err)
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
	if metrics.Delivered != 0 {
		t.Fatalf("expected delivered 0 got %d", metrics.Delivered)
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

	// Poll for cleanup
	deadline := time.Now().Add(time.Second)
	cleaned := false
	for time.Now().Before(deadline) {
		subs, _ := bus.subs.Get("key")
		if len(subs) == 0 {
			cleaned = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !cleaned {
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

	// Manually set pending
	bus.pending.Set("key", true)

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

	if bus.subs.Has("key") {
		subs, _ := bus.subs.Get("key")
		if len(subs) > 0 {
			t.Fatal("subscription should not be added when context is canceled")
		}
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

	if !bus.subs.Has("key") {
		t.Fatal("subscription subscription should remain when unsubscribe context is canceled")
	}
	subs, _ := bus.subs.Get("key")
	found := false
	for _, c := range subs {
		if c == ch {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("subscription channel missing")
	}

	if err := bus.Unsubscribe(context.Background(), "key", ch); err != nil {
		t.Fatalf("cleanup unsubscribe: %v", err)
	}
}

func TestLeaseRevokeFlowAndMetrics(t *testing.T) {
	bus := NewInMemoryBus()
	ctx := context.Background()
	ch, err := bus.SubscribeLease(ctx, "id")
	if err != nil {
		t.Fatalf("subscribe lease: %v", err)
	}

	if err := bus.RevokeLease(context.Background(), "id"); err != nil {
		t.Fatalf("revoke lease: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for revoke lease")
	}

	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected published 1 got %d", metrics.Published)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected delivered 1 got %d", metrics.Delivered)
	}
}

func TestSubscribeLeaseContextCanceled(t *testing.T) {
	bus := NewInMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bus.SubscribeLease(ctx, "id"); err == nil {
		t.Fatal("expected subscribe lease error due to canceled context")
	}

	subs, _ := bus.subs.Get("lease:id")
	if len(subs) > 0 {
		t.Fatal("subscription should not be added when context is canceled")
	}
}

func TestUnsubscribeLeaseClosesChannel(t *testing.T) {
	bus := NewInMemoryBus()
	ch, err := bus.SubscribeLease(context.Background(), "id")
	if err != nil {
		t.Fatalf("subscribe lease: %v", err)
	}
	if err := bus.UnsubscribeLease(context.Background(), "id", ch); err != nil {
		t.Fatalf("unsubscribe lease: %v", err)
	}

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for unsubscribe lease")
	}

	subs, _ := bus.subs.Get("lease:id")
	if len(subs) > 0 {
		t.Fatal("subscription still present after unsubscribe lease")
	}

	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
	if metrics.Delivered != 0 {
		t.Fatalf("expected delivered 0 got %d", metrics.Delivered)
	}
}

func TestRevokeLeaseContextCanceled(t *testing.T) {
	bus := NewInMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bus.RevokeLease(ctx, "id"); err == nil {
		t.Fatal("expected revoke lease error due to canceled context")
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
	if metrics.Delivered != 0 {
		t.Fatalf("expected delivered 0 got %d", metrics.Delivered)
	}
}

func TestUnsubscribeLeaseContextCanceled(t *testing.T) {
	bus := NewInMemoryBus()
	ch, err := bus.SubscribeLease(context.Background(), "id")
	if err != nil {
		t.Fatalf("subscribe lease: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bus.UnsubscribeLease(ctx, "id", ch); err == nil {
		t.Fatal("expected unsubscribe lease error due to canceled context")
	}

	subs, _ := bus.subs.Get("lease:id")
	if len(subs) == 0 {
		t.Fatal("subscription should remain when unsubscribe lease context is canceled")
	}

	if err := bus.UnsubscribeLease(context.Background(), "id", ch); err != nil {
		t.Fatalf("cleanup unsubscribe lease: %v", err)
	}
}
