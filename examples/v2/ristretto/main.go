package main

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/mirkobrombin/go-warp/v2/adapter"
	"github.com/mirkobrombin/go-warp/v2/cache"
	"github.com/mirkobrombin/go-warp/v2/core"
	"github.com/mirkobrombin/go-warp/v2/merge"
)

func main() {
	ctx := context.Background()

	cfg := &ristretto.Config{NumCounters: 1e5, MaxCost: 1 << 20, BufferItems: 64}
	c := cache.NewRistretto[merge.Value[string]](cache.WithRistretto(cfg))

	w := core.New[string](c, adapter.NewInMemoryStore[string](), nil, merge.NewEngine[string]())
	w.Register("greeting", core.ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "greeting", "Warp example"); err != nil {
		panic(err)
	}
	v, _ := w.Get(ctx, "greeting")
	fmt.Println(v)
}
