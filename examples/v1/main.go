package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

func main() {
	ctx := context.Background()
	w := core.New(cache.NewInMemory(), adapter.NewInMemoryStore(), nil, merge.NewEngine())
	w.Register("greeting", core.ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "greeting", "Warp example"); err != nil {
		panic(err)
	}
	v, _ := w.Get(ctx, "greeting")
	fmt.Println(v)
}
