package syncbus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

type failingBus struct {
	publishErr   error
	subscribeErr error
}

func (f *failingBus) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	return f.publishErr
}
func (b *failingBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	return b.publishErr
}

func (b *failingBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	return b.publishErr
}

func (f *failingBus) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	return nil, f.subscribeErr
}
func (f *failingBus) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
	return nil
}
func (f *failingBus) RevokeLease(ctx context.Context, id string) error { return f.publishErr }
func (f *failingBus) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return nil, f.subscribeErr
}
func (f *failingBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return nil
}

func (b *failingBus) IsHealthy() bool {
	return true
}

func TestWarpPublishError(t *testing.T) {
	bus := &failingBus{publishErr: errors.New("publish failed")}
	w := core.New[string](cache.NewInMemory[merge.Value[string]](), nil, bus, merge.NewEngine[string]())
	w.Register("key", core.ModeEventualDistributed, time.Minute)
	if err := w.Invalidate(context.Background(), "key"); err != nil {
		t.Fatalf("unexpected invalidate error: %v", err)
	}
	select {
	case err := <-w.PublishErrors():
		if !errors.Is(err, bus.publishErr) {
			t.Fatalf("expected %v got %v", bus.publishErr, err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected publish error")
	}
}

func TestWarpSubscribeError(t *testing.T) {
	bus := &failingBus{subscribeErr: errors.New("subscribe failed")}
	w := core.New[string](cache.NewInMemory[merge.Value[string]](), nil, bus, merge.NewEngine[string]())
	if _, err := w.GrantLease(context.Background(), time.Minute); err != nil {
		t.Fatalf("grant lease returned error: %v", err)
	}
}
