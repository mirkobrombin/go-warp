package syncbus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

type failingBus struct {
	publishErr   error
	subscribeErr error
}

func (f *failingBus) Publish(ctx context.Context, key string) error { return f.publishErr }
func (f *failingBus) Subscribe(ctx context.Context, key string) (chan struct{}, error) {
	return nil, f.subscribeErr
}
func (f *failingBus) Unsubscribe(ctx context.Context, key string, ch chan struct{}) error { return nil }
func (f *failingBus) RevokeLease(ctx context.Context, id string) error                    { return f.publishErr }
func (f *failingBus) SubscribeLease(ctx context.Context, id string) (chan struct{}, error) {
	return nil, f.subscribeErr
}
func (f *failingBus) UnsubscribeLease(ctx context.Context, id string, ch chan struct{}) error {
	return nil
}

func TestWarpPublishError(t *testing.T) {
	bus := &failingBus{publishErr: errors.New("publish failed")}
	w := core.New[string](cache.NewInMemory[merge.Value[string]](), nil, bus, merge.NewEngine[string]())
	w.Register("key", core.ModeStrongDistributed, time.Minute)
	if err := w.Invalidate(context.Background(), "key"); err == nil {
		t.Fatal("expected invalidate error when bus publish fails")
	}
}

func TestWarpSubscribeError(t *testing.T) {
	bus := &failingBus{subscribeErr: errors.New("subscribe failed")}
	w := core.New[string](cache.NewInMemory[merge.Value[string]](), nil, bus, merge.NewEngine[string]())
	if _, err := w.GrantLease(context.Background(), time.Minute); err != nil {
		t.Fatalf("grant lease returned error: %v", err)
	}
}
