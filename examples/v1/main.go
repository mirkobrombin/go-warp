package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/watchbus"
)

func main() {
	ctx := context.Background()
	w := core.New[string](cache.NewInMemory[merge.Value[string]](), adapter.NewInMemoryStore[string](), nil, merge.NewEngine[string]())
	w.Register("greeting", core.ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "greeting", "Warp example"); err != nil {
		panic(err)
	}
	v, _ := w.Get(ctx, "greeting")
	fmt.Println(v)

	// WatchBus prefix example
	bus := watchbus.NewInMemory()
	ch, err := core.WatchPrefix(ctx, bus, "user:")
	if err != nil {
		panic(err)
	}
	go func() {
		for msg := range ch {
			fmt.Printf("prefix event: %s\n", msg)
		}
	}()
	_ = bus.Publish(ctx, "user:1", []byte("created"))
	_ = bus.PublishPrefix(ctx, "user:", []byte("updated"))
	time.Sleep(100 * time.Millisecond)
}
