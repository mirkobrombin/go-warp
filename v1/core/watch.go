package core

import (
	"context"

	"github.com/mirkobrombin/go-warp/v1/metrics"
	"github.com/mirkobrombin/go-warp/v1/watchbus"
)

// WatchPrefix wraps watchbus.SubscribePrefix to expose prefix watching from core.
func WatchPrefix(ctx context.Context, bus watchbus.WatchBus, prefix string) (chan []byte, error) {
	ch, err := bus.SubscribePrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	metrics.WatcherGauge.Inc()
	go func() {
		<-ctx.Done()
		metrics.WatcherGauge.Dec()
	}()
	return ch, nil
}
