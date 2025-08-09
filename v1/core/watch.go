package core

import (
	"context"
	"github.com/mirkobrombin/go-warp/v1/watchbus"
)

// WatchPrefix wraps watchbus.SubscribePrefix to expose prefix watching from core.
func WatchPrefix(ctx context.Context, bus watchbus.WatchBus, prefix string) (chan []byte, error) {
	return bus.SubscribePrefix(ctx, prefix)
}
