package syncbus

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockBus struct {
	publishFunc func(ctx context.Context, key string, opts ...PublishOption) error
	isHealthy   bool
	*InMemoryBus
}

func (m *mockBus) Publish(ctx context.Context, key string, opts ...PublishOption) error {
	if m.publishFunc != nil {
		return m.publishFunc(ctx, key, opts...)
	}
	return m.InMemoryBus.Publish(ctx, key, opts...)
}

func (m *mockBus) IsHealthy() bool { return m.isHealthy }

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	mb := &mockBus{InMemoryBus: NewInMemoryBus(), isHealthy: true}
	threshold := 2
	timeout := 50 * time.Millisecond
	cb := NewCircuitBreaker(mb, threshold, timeout)

	ctx := context.Background()
	failErr := errors.New("fail")

	if !cb.IsHealthy() {
		t.Fatal("expected healthy initially")
	}

	mb.publishFunc = func(ctx context.Context, key string, opts ...PublishOption) error { return failErr }
	if err := cb.Publish(ctx, "key"); err != failErr {
		t.Fatalf("expected failErr, got %v", err)
	}
	if !cb.IsHealthy() {
		t.Fatal("expected healthy after 1 failure (threshold 2)")
	}

	if err := cb.Publish(ctx, "key"); err != failErr {
		t.Fatalf("expected failErr, got %v", err)
	}
	if cb.IsHealthy() {
		t.Fatal("expected unhealthy/open after threshold reached")
	}
	if err := cb.Publish(ctx, "key"); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	time.Sleep(timeout + 10*time.Millisecond)

	if !cb.IsHealthy() {
		t.Fatal("expected healthy (time passed)")
	}

	mb.publishFunc = func(ctx context.Context, key string, opts ...PublishOption) error { return nil }
	if err := cb.Publish(ctx, "key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cb.IsHealthy() {
		t.Fatal("expected healthy after success")
	}
	if cb.failures != 0 {
		t.Fatalf("expected failures=0, got %d", cb.failures)
	}

	mb.publishFunc = func(ctx context.Context, key string, opts ...PublishOption) error { return failErr }
	cb.Publish(ctx, "key")
	cb.Publish(ctx, "key")
	if cb.IsHealthy() {
		t.Fatal("expected open")
	}

	time.Sleep(timeout + 10*time.Millisecond)
	if err := cb.Publish(ctx, "key"); err != failErr {
		t.Fatalf("expected failErr, got %v", err)
	}
	if cb.IsHealthy() {
		t.Fatal("expected open after half-open failure")
	}
	if err := cb.Publish(ctx, "key"); err != ErrCircuitOpen {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_Passthrough(t *testing.T) {
	// Re-using InMemoryBus directly embedded in mock
	mb := &mockBus{InMemoryBus: NewInMemoryBus(), isHealthy: true}
	cb := NewCircuitBreaker(mb, 5, time.Minute)

	ctx := context.Background()
	// Test Publish
	if err := cb.Publish(ctx, "foo"); err != nil {
		t.Fatal(err)
	}

	// Ensure it went through to underlying bus
	sub, _ := mb.InMemoryBus.Subscribe(ctx, "foo")
	go func() {
		cb.Publish(ctx, "foo")
	}()
	select {
	case <-sub:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on underlying bus")
	}
}
