package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

// MockBusBlocked implements syncbus.Bus and blocks on Publish until signaled
type MockBusBlocked struct {
	blockCh chan struct{}
}

func (m *MockBusBlocked) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	select {
	case <-m.blockCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *MockBusBlocked) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	return nil, nil
}

func (m *MockBusBlocked) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
	return nil
}

func (m *MockBusBlocked) IsHealthy() bool {
	return true
}

func (m *MockBusBlocked) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	return nil
}

func (m *MockBusBlocked) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	return nil
}

func (m *MockBusBlocked) RevokeLease(ctx context.Context, id string) error {
	return nil
}

func (m *MockBusBlocked) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return nil, nil
}

func (m *MockBusBlocked) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return nil
}

func (m *MockBusBlocked) SetTopology(minZones int) {}

func (m *MockBusBlocked) Peers() []string {
	return nil
}

func TestBackplane_TimeoutPreventsLeak(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))

	// Create a bus that blocks forever
	bus := &MockBusBlocked{
		blockCh: make(chan struct{}), // Never closed in this test path
	}

	w := New[string](c, nil, bus, nil, WithPublishTimeout[string](50*time.Millisecond))
	w.Register("bp-key-timeout", ModeEventualDistributed, 10*time.Minute)

	// Set triggers background publish
	err := w.Set(context.Background(), "bp-key-timeout", "val")
	if err != nil {
		t.Fatalf("Expected success on Set, got: %v", err)
	}

	// Wait for timeout error on channel
	select {
	case err := <-w.PublishErrors():
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Expected DeadlineExceeded, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Timed out waiting for publish error")
	}
}

func TestBackplane_NoTimeoutByDefault(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))

	bus := &MockBusBlocked{
		blockCh: make(chan struct{}),
	}

	// No timeout configured
	w := New[string](c, nil, bus, nil)
	w.Register("bp-key-default", ModeEventualDistributed, 10*time.Minute)

	w.Set(context.Background(), "bp-key-default", "val")

	select {
	case err := <-w.PublishErrors():
		t.Errorf("Unexpected error received (should be blocked): %v", err)
	case <-time.After(100 * time.Millisecond):
		// Success: it's still blocked/running, logic is correct for no timeout
	}
}
