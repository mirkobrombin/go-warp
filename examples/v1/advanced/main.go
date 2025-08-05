package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func main() {
	ctx := context.Background()
	store := adapter.NewInMemoryStore()
	bus := syncbus.NewInMemoryBus()
	engine := merge.NewEngine()
	engine.Register("counter", func(old, new any) (any, error) {
		return old.(int) + new.(int), nil
	})

	w1 := core.New(cache.NewInMemory(), store, bus, engine)
	w2 := core.New(cache.NewInMemory(), store, bus, engine)

	w1.Register("counter", core.ModeEventualDistributed, time.Minute)
	w2.Register("counter", core.ModeEventualDistributed, time.Minute)

	ch, _ := bus.Subscribe(ctx, "counter")
	defer bus.Unsubscribe(ctx, "counter", ch)
	go func() {
		for range ch {
			_ = w2.Invalidate(ctx, "counter")
		}
	}()

	if err := w1.Set(ctx, "counter", 10); err != nil {
		panic(err)
	}
	if err := w2.Set(ctx, "counter", 5); err != nil {
		panic(err)
	}

	time.Sleep(10 * time.Millisecond)
	v1, _ := w1.Get(ctx, "counter")
	v2, _ := w2.Get(ctx, "counter")
	fmt.Println("node1:", v1, "node2:", v2)
}
