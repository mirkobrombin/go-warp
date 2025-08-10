package main

import (
	"context"
	"fmt"
	"log"
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
				v, ok, err := store.Get(ctx, "counter")
				if err != nil {
					log.Printf("store.Get error: %v", err)
					mu.Unlock()
					return
				}
				if !ok {
					v = 0
				}
				if err := store.Set(ctx, "counter", v+1); err != nil {
					log.Printf("store.Set error: %v", err)
					mu.Unlock()
					return
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	val, _, err := store.Get(ctx, "counter")
	if err != nil {
		log.Printf("store.Get error: %v", err)
		return 0, 0, 0
	}
	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("runWithoutWarp took %s, throughput %.2f ops/s\n", elapsed, throughput)
	return val, elapsed, throughput
}

func runWithWarp(ctx context.Context) (int, time.Duration, float64) {
	// ctx should be derived with context.WithCancel or context.WithTimeout so that
	// all goroutines spawned in this function are canceled if main exits early.
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
	leaseID, err := w.GrantLease(ctx, time.Second)
	if err != nil {
		log.Printf("GrantLease error: %v", err)
		return 0, 0, 0
	}
	w.AttachKey(leaseID, "counter")

	// validator
	v := w.Validator(validator.ModeAutoHeal, 100*time.Millisecond)
	vCtx, cancelValidator := context.WithCancel(ctx)

	// watch bus
	watchCtx, cancelWatch := context.WithCancel(ctx)
	watchCh, err := core.WatchPrefix(watchCtx, wb, "event")
	if err != nil {
		log.Printf("WatchPrefix error: %v", err)
		cancelWatch()
		cancelValidator()
		return 0, 0, 0
	}

	// ensure goroutines end before returning
	var asyncWG sync.WaitGroup
	errCh := make(chan error, 2)

	asyncWG.Add(1)
	go func() {
		defer asyncWG.Done()
		v.Run(vCtx)
		if err := vCtx.Err(); err != nil && err != context.Canceled {
			errCh <- fmt.Errorf("validator error: %w", err)
		}
	}()

	asyncWG.Add(1)
	go func() {
		defer asyncWG.Done()
		for {
			select {
			case <-watchCtx.Done():
				if err := watchCtx.Err(); err != nil && err != context.Canceled {
					errCh <- fmt.Errorf("watch error: %w", err)
				}
				return
			case msg, ok := <-watchCh:
				if !ok {
					return
				}
				fmt.Println("watch:", string(msg))
			}
		}
	}()

	defer func() {
		cancelWatch()
		cancelValidator()
		asyncWG.Wait()
		close(errCh)
		for err := range errCh {
			log.Printf("async error: %v", err)
		}
	}()

	// lock
	locker := lock.NewInMemory(bus)
	if err := locker.Acquire(ctx, "counter", 0); err != nil {
		log.Printf("locker.Acquire error: %v", err)
		return 0, 0, 0
	}

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
				if err := txn.Commit(); err != nil {
					log.Printf("txn.Commit error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := locker.Release(ctx, "counter"); err != nil {
		log.Printf("locker.Release error: %v", err)
	}

	if err := wb.Publish(ctx, "event/warp", []byte("done")); err != nil {
		log.Printf("wb.Publish error: %v", err)
	}

	val, err := w.Get(ctx, "counter")
	if err != nil {
		log.Printf("w.Get error: %v", err)
		return 0, 0, 0
	}
	past, err := w.GetAt(ctx, "counter", time.Now().Add(-time.Millisecond))
	if err != nil {
		log.Printf("w.GetAt error: %v", err)
		return 0, 0, 0
	}
	fmt.Println("current:", val, "past:", past)

	if err := w.Invalidate(ctx, "counter"); err != nil {
		log.Printf("w.Invalidate error: %v", err)
	}
	w.RevokeLease(ctx, leaseID)
	w.Unregister("counter")

	// expose metrics
	mfs, err := reg.Gather()
	if err != nil {
		log.Printf("reg.Gather error: %v", err)
	} else {
		for _, mf := range mfs {
			for _, m := range mf.Metric {
				if m.Counter != nil {
					fmt.Printf("metric %s %f\n", mf.GetName(), m.GetCounter().GetValue())
				} else if m.Gauge != nil {
					fmt.Printf("metric %s %f\n", mf.GetName(), m.GetGauge().GetValue())
				}
			}
		}
	}

	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("runWithWarp took %s, throughput %.2f ops/s\n", elapsed, throughput)

	return val, elapsed, throughput
}

func main() {
	// Use a cancelable context with a timeout to avoid leaking goroutines
	// if main exits before the examples complete.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
