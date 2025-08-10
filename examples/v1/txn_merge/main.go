package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

func main() {
	ctx := context.Background()
	w := core.New[any](cache.NewInMemory[merge.Value[any]](), adapter.NewInMemoryStore[any](), nil, merge.NewEngine[any]())
	w.Register("counter", core.ModeStrongLocal, time.Minute)
	w.Register("logs", core.ModeStrongLocal, time.Minute)

	w.Merge("counter", func(old, new any) (any, error) {
		return old.(int) + new.(int), nil
	})
	w.Merge("logs", func(old, new any) (any, error) {
		return append(old.([]string), new.([]string)...), nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txn := w.Txn(ctx)
			txn.Set("counter", 1)
			txn.Set("logs", []string{fmt.Sprintf("event %d", i)})
			if err := txn.Commit(); err != nil {
				panic(err)
			}
		}(i)
	}
	wg.Wait()

	counter, _ := w.Get(ctx, "counter")
	logs, _ := w.Get(ctx, "logs")
	fmt.Printf("counter: %d\n", counter.(int))
	fmt.Printf("logs: %v\n", logs.([]string))
}
