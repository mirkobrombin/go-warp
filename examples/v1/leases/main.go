package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

func main() {
	ctx := context.Background()
	w := core.New[string](cache.NewInMemory[merge.Value[string]](), nil, nil, merge.NewEngine[string]())

	w.Register("task", core.ModeStrongLocal, time.Minute)
	id, err := w.GrantLease(ctx, time.Second)
	if err != nil {
		panic(err)
	}
	w.AttachKey(id, "task")

	_ = w.Set(ctx, "task", "running")
	v, _ := w.Get(ctx, "task")
	fmt.Println("before revoke:", v)

	w.RevokeLease(ctx, id)
	if _, err := w.Get(ctx, "task"); err != nil {
		fmt.Println("after revoke: not found")
	}
}
