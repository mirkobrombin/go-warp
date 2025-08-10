package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/cache/versioned"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

func main() {
	ctx := context.Background()
	base := cache.NewInMemory[merge.VersionedValue[string]]()
	c := versioned.New[string](base, 5)
	w := core.New[string](c, nil, nil, merge.NewEngine[string]())

	w.Register("status", core.ModeStrongLocal, time.Minute)
	_ = w.Set(ctx, "status", "v1")
	t1 := time.Now()
	time.Sleep(10 * time.Millisecond)
	_ = w.Set(ctx, "status", "v2")

	latest, _ := w.Get(ctx, "status")
	fmt.Println("latest:", latest)

	old, err := w.GetAt(ctx, "status", t1)
	if err != nil {
		panic(err)
	}
	fmt.Println("at t1:", old)
}
