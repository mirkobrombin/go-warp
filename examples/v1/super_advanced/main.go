package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/cache/adaptive"
	"github.com/mirkobrombin/go-warp/v1/cache/versioned"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/lock"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/metrics"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	"github.com/mirkobrombin/go-warp/v1/validator"
	"github.com/mirkobrombin/go-warp/v1/watchbus"
)

// workerCount and opsPerWorker control the amount of stress applied.
const (
	workerCount  = 8
	opsPerWorker = 500
)

func runWithoutWarp(ctx context.Context) (int, time.Duration, float64) {
	start := time.Now()
	store := adapter.NewInMemoryStore[int]()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				mu.Lock()
				v, ok, _ := store.Get(ctx, "counter")
				if !ok {
					v = 0
				}
				_ = store.Set(ctx, "counter", v+1)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	val, _, _ := store.Get(ctx, "counter")
	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("runWithoutWarp took %s, throughput %.2f ops/s\n", elapsed, throughput)
	return val, elapsed, throughput
}

func runWithWarp(ctx context.Context) (int, time.Duration, float64) {
	start := time.Now()
	reg := metrics.NewRegistry()
	metrics.RegisterCoreMetrics(reg)

	store := adapter.NewInMemoryStore[int]()
	bus := syncbus.NewInMemoryBus()
	wb := watchbus.NewInMemory()

	baseCache := cache.NewInMemory[merge.VersionedValue[int]]()
	vCache := versioned.New[int](baseCache, 10, versioned.WithMetrics[int](reg))
	engine := merge.NewEngine[int]()
	w := core.New[int](vCache, store, bus, engine, core.WithMetrics[int](reg))

	// merge logic to sum values
	w.Merge("counter", func(old, new int) (int, error) { return old + new, nil })

	// register keys
	w.Register("counter", core.ModeEventualDistributed, time.Second,
		cache.WithSliding(), cache.WithDynamicTTL(10*time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Second))
	strat := adaptive.NewSlidingWindow(50*time.Millisecond, 20, time.Millisecond, 2*time.Second, reg)
	w.RegisterDynamicTTL("hot", core.ModeStrongLocal, strat)

	// warmup and lease
	w.Warmup(ctx)
	leaseID, _ := w.GrantLease(ctx, time.Second)
	w.AttachKey(leaseID, "counter")

	// validator
	v := w.Validator(validator.ModeAutoHeal, 100*time.Millisecond)
	vCtx, cancelValidator := context.WithCancel(ctx)
	go v.Run(vCtx)

	// watch bus
	watchCtx, cancelWatch := context.WithCancel(ctx)
	watchCh, _ := core.WatchPrefix(watchCtx, wb, "event")
	go func() {
		for msg := range watchCh {
			fmt.Println("watch:", string(msg))
		}
	}()

	// lock
	locker := lock.NewInMemory(bus)
	_ = locker.Acquire(ctx, "counter", 0)

	// stress with Txn
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				txn := w.Txn(ctx)
				txn.Set("counter", 1)
				txn.CompareAndSwap("hot", 0, 1) // no-op but exercises CAS
				_ = txn.Commit()
			}
		}()
	}
	wg.Wait()
	_ = locker.Release(ctx, "counter")

	wb.Publish(ctx, "event/warp", []byte("done"))

	val, _ := w.Get(ctx, "counter")
	past, _ := w.GetAt(ctx, "counter", time.Now().Add(-time.Millisecond))
	fmt.Println("current:", val, "past:", past)

	_ = w.Invalidate(ctx, "counter")
	w.RevokeLease(ctx, leaseID)
	w.Unregister("counter")
	cancelWatch()
	cancelValidator()

	// expose metrics
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		for _, m := range mf.Metric {
			if m.Counter != nil {
				fmt.Printf("metric %s %f\n", mf.GetName(), m.GetCounter().GetValue())
			} else if m.Gauge != nil {
				fmt.Printf("metric %s %f\n", mf.GetName(), m.GetGauge().GetValue())
			}
		}
	}

	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("runWithWarp took %s, throughput %.2f ops/s\n", elapsed, throughput)

	return val, elapsed, throughput
}

func main() {
	ctx := context.Background()
	withoutVal, withoutElapsed, withoutThroughput := runWithoutWarp(ctx)
	withVal, withElapsed, withThroughput := runWithWarp(ctx)
	fmt.Println("without warp:", withoutVal)
	fmt.Println("with warp:", withVal)
	fmt.Printf("time: without %s vs with %s\n", withoutElapsed, withElapsed)
	fmt.Printf("throughput: without %.2f ops/s vs with %.2f ops/s\n", withoutThroughput, withThroughput)
	if withElapsed < withoutElapsed {
		fmt.Printf("warp speedup: %.2fx faster, throughput gain: %.2fx\n", withoutElapsed.Seconds()/withElapsed.Seconds(), withThroughput/withoutThroughput)
	}
}
