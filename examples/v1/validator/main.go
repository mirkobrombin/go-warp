package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/validator"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := adapter.NewInMemoryStore[string]()
	c := cache.NewInMemory[merge.Value[string]]()
	w := core.New[string](c, store, nil, merge.NewEngine[string]())
	w.Register("greeting", core.ModeStrongLocal, time.Minute)

	_ = w.Set(ctx, "greeting", "hello")
	_ = store.Set(ctx, "greeting", "hi")

	v := w.Validator(validator.ModeAutoHeal, 20*time.Millisecond)
	go v.Run(ctx)

	time.Sleep(50 * time.Millisecond)
	val, _ := w.Get(ctx, "greeting")
	fmt.Println("healed value:", val)
	fmt.Println("mismatches:", v.Metrics())
}
