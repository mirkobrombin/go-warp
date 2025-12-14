package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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

type runStats struct {
	name       string
	val        int
	elapsed    time.Duration
	throughput float64
}

// fileStore is a simple disk-backed store introducing real I/O overhead
// for each operation to highlight Warp's caching benefits.
type fileStore struct {
	dir   string
	delay time.Duration
	mu    sync.Mutex
}

func newFileStore(dir string, delay time.Duration) *fileStore {
	os.MkdirAll(dir, 0o755)
	return &fileStore{dir: dir, delay: delay}
}

func (s *fileStore) path(key string) string {
	return filepath.Join(s.dir, key+".txt")
}

func (s *fileStore) Get(ctx context.Context, key string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	v, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, false, err
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return v, true, nil
}

func (s *fileStore) Set(ctx context.Context, key string, value int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.WriteFile(s.path(key), []byte(strconv.Itoa(value)), 0o644); err != nil {
		return err
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return nil
}

func (s *fileStore) Keys(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			keys = append(keys, strings.TrimSuffix(e.Name(), ".txt"))
		}
	}
	return keys, nil
}

func runWithoutWarp(ctx context.Context) runStats {
	fmt.Println("=== Without Warp ===")
	dir, err := os.MkdirTemp("", "warp_store")
	if err != nil {
		log.Printf("MkdirTemp error: %v", err)
		return runStats{name: "without warp"}
	}
	defer os.RemoveAll(dir)
	store := newFileStore(dir, 500*time.Microsecond)
	if err := store.Set(ctx, "counter", 0); err != nil {
		log.Printf("store.Set error: %v", err)
		return runStats{name: "without warp"}
	}

	start := time.Now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range opsPerWorker {
				mu.Lock()
				v, _, err := store.Get(ctx, "counter")
				if err != nil {
					log.Printf("store.Get error: %v", err)
					mu.Unlock()
					return
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
		return runStats{name: "without warp"}
	}
	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("value=%d elapsed=%s throughput=%.2f ops/s\n", val, elapsed, throughput)
	return runStats{name: "without warp", val: val, elapsed: elapsed, throughput: throughput}
}

func runWithWarp(ctx context.Context) runStats {
	fmt.Println("=== With Warp ===")
	dir, err := os.MkdirTemp("", "warp_store")
	if err != nil {
		log.Printf("MkdirTemp error: %v", err)
		return runStats{name: "with warp"}
	}
	defer os.RemoveAll(dir)
	store := newFileStore(dir, 500*time.Microsecond)
	if err := store.Set(ctx, "counter", 0); err != nil {
		log.Printf("store.Set error: %v", err)
		return runStats{name: "with warp"}
	}

	start := time.Now()
	reg := metrics.NewRegistry()
	metrics.RegisterCoreMetrics(reg)

	bus := syncbus.NewInMemoryBus()

	baseCache := cache.NewInMemory[merge.VersionedValue[int]]()
	vCache := versioned.New(baseCache, workerCount*opsPerWorker, versioned.WithMetrics[int](reg))
	engine := merge.NewEngine[int]()
	w := core.New(vCache, store, bus, engine, core.WithMetrics[int](reg))

	w.Merge("counter", func(old, new int) (int, error) { return old + new, nil })

	w.Register("counter", core.ModeStrongLocal, time.Second, cache.WithSliding())
	strat := adaptive.NewSlidingWindow(50*time.Millisecond, 20, time.Millisecond, 2*time.Second, reg)
	w.RegisterDynamicTTL("hot", core.ModeStrongLocal, strat)

	w.Warmup(ctx)
	if err := w.Set(ctx, "hot", 1); err != nil {
		log.Printf("w.Set hot error: %v", err)
		return runStats{name: "with warp"}
	}
	leaseID, err := w.GrantLease(ctx, time.Second)
	if err != nil {
		log.Printf("GrantLease error: %v", err)
		return runStats{name: "with warp"}
	}
	w.AttachKey(leaseID, "counter")

	var wg sync.WaitGroup
	var mu sync.Mutex
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range opsPerWorker {
				txn := w.Txn(ctx)
				txn.Set("counter", 1)
				mu.Lock()
				if err := txn.Commit(); err != nil {
					log.Printf("txn.Commit error: %v", err)
					mu.Unlock()
					return
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	val, err := w.Get(ctx, "counter")
	if err != nil {
		log.Printf("w.Get error: %v", err)
		return runStats{name: "with warp"}
	}
	past, err := w.GetAt(ctx, "counter", time.Now().Add(-time.Millisecond))
	if err != nil {
		log.Printf("w.GetAt error: %v", err)
		return runStats{name: "with warp"}
	}
	fmt.Println("current:", val, "past:", past)

	if err := w.Invalidate(ctx, "counter"); err != nil {
		log.Printf("w.Invalidate error: %v", err)
	}
	w.RevokeLease(ctx, leaseID)
	w.Unregister("counter")

	mfs, err := reg.Gather()
	if err != nil {
		log.Printf("reg.Gather error: %v", err)
	} else {
		fmt.Println("metrics:")
		for _, mf := range mfs {
			for _, m := range mf.Metric {
				if m.Counter != nil {
					fmt.Printf("  %s %f\n", mf.GetName(), m.GetCounter().GetValue())
				} else if m.Gauge != nil {
					fmt.Printf("  %s %f\n", mf.GetName(), m.GetGauge().GetValue())
				}
			}
		}
	}

	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("value=%d elapsed=%s throughput=%.2f ops/s\n", val, elapsed, throughput)

	return runStats{name: "with warp", val: val, elapsed: elapsed, throughput: throughput}
}

func runWithWarpFull(ctx context.Context) runStats {
	fmt.Println("=== With Warp + Validator & Bus ===")
	dir, err := os.MkdirTemp("", "warp_store")
	if err != nil {
		log.Printf("MkdirTemp error: %v", err)
		return runStats{name: "warp full"}
	}
	defer os.RemoveAll(dir)
	store := newFileStore(dir, 500*time.Microsecond)
	if err := store.Set(ctx, "counter", 0); err != nil {
		log.Printf("store.Set error: %v", err)
		return runStats{name: "warp full"}
	}

	start := time.Now()
	reg := metrics.NewRegistry()
	metrics.RegisterCoreMetrics(reg)

	bus := syncbus.NewInMemoryBus()
	wb := watchbus.NewInMemory()

	baseCache := cache.NewInMemory[merge.VersionedValue[int]]()
	vCache := versioned.New(baseCache, workerCount*opsPerWorker, versioned.WithMetrics[int](reg))
	engine := merge.NewEngine[int]()
	w := core.New(vCache, store, bus, engine, core.WithMetrics[int](reg))

	w.Merge("counter", func(old, new int) (int, error) { return old + new, nil })

	w.Register("counter", core.ModeStrongLocal, time.Second, cache.WithSliding())
	strat := adaptive.NewSlidingWindow(50*time.Millisecond, 20, time.Millisecond, 2*time.Second, reg)
	w.RegisterDynamicTTL("hot", core.ModeStrongLocal, strat)

	w.Warmup(ctx)
	if err := w.Set(ctx, "hot", 1); err != nil {
		log.Printf("w.Set hot error: %v", err)
		return runStats{name: "warp full"}
	}
	leaseID, err := w.GrantLease(ctx, time.Second)
	if err != nil {
		log.Printf("GrantLease error: %v", err)
		return runStats{name: "warp full"}
	}
	w.AttachKey(leaseID, "counter")

	v := w.Validator(validator.ModeAutoHeal, 100*time.Millisecond)

	watchCtx, cancelWatch := context.WithCancel(ctx)
	watchCh, err := core.WatchPrefix(watchCtx, wb, "event")
	if err != nil {
		log.Printf("WatchPrefix error: %v", err)
		cancelWatch()
		return runStats{name: "warp full"}
	}

	var watchWG sync.WaitGroup
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		for {
			select {
			case <-watchCtx.Done():
				if err := watchCtx.Err(); err != nil && err != context.Canceled {
					log.Printf("watch error: %v", err)
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
		watchWG.Wait()
	}()

	locker := lock.NewInMemory(bus)
	if err := locker.Acquire(ctx, "counter", 0); err != nil {
		log.Printf("locker.Acquire error: %v", err)
		return runStats{name: "warp full"}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range opsPerWorker {
				txn := w.Txn(ctx)
				txn.Set("counter", 1)
				mu.Lock()
				if err := txn.Commit(); err != nil {
					log.Printf("txn.Commit error: %v", err)
					mu.Unlock()
					return
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	valCtx, cancelVal := context.WithCancel(ctx)
	go v.Run(valCtx)
	time.Sleep(150 * time.Millisecond)
	cancelVal()

	if err := locker.Release(ctx, "counter"); err != nil {
		log.Printf("locker.Release error: %v", err)
	}

	// allow validator to flush latest store state
	time.Sleep(200 * time.Millisecond)

	if err := wb.Publish(ctx, "event/warp", []byte("done")); err != nil {
		log.Printf("wb.Publish error: %v", err)
	}

	val, _, err := store.Get(ctx, "counter")
	if err != nil {
		log.Printf("store.Get error: %v", err)
		return runStats{name: "warp full"}
	}
	past, err := w.GetAt(ctx, "counter", time.Now().Add(-time.Millisecond))
	if err != nil {
		log.Printf("w.GetAt error: %v", err)
		return runStats{name: "warp full"}
	}
	fmt.Println("current:", val, "past:", past)

	if err := w.Invalidate(ctx, "counter"); err != nil {
		log.Printf("w.Invalidate error: %v", err)
	}
	w.RevokeLease(ctx, leaseID)
	w.Unregister("counter")

	mfs, err := reg.Gather()
	if err != nil {
		log.Printf("reg.Gather error: %v", err)
	} else {
		fmt.Println("metrics:")
		for _, mf := range mfs {
			for _, m := range mf.Metric {
				if m.Counter != nil {
					fmt.Printf("  %s %f\n", mf.GetName(), m.GetCounter().GetValue())
				} else if m.Gauge != nil {
					fmt.Printf("  %s %f\n", mf.GetName(), m.GetGauge().GetValue())
				}
			}
		}
	}

	elapsed := time.Since(start)
	throughput := float64(opsPerWorker*workerCount) / elapsed.Seconds()
	fmt.Printf("value=%d elapsed=%s throughput=%.2f ops/s\n", val, elapsed, throughput)

	return runStats{name: "warp full", val: val, elapsed: elapsed, throughput: throughput}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	without := runWithoutWarp(ctx)
	withOnly := runWithWarp(ctx)
	withFull := runWithWarpFull(ctx)

	fmt.Println("\n=== Summary ===")
	fmt.Printf("without warp: value=%d elapsed=%s throughput=%.2f ops/s\n", without.val, without.elapsed, without.throughput)
	fmt.Printf("with warp: value=%d elapsed=%s throughput=%.2f ops/s\n", withOnly.val, withOnly.elapsed, withOnly.throughput)
	fmt.Printf("with warp + extras: value=%d elapsed=%s throughput=%.2f ops/s\n", withFull.val, withFull.elapsed, withFull.throughput)

	if withOnly.elapsed > 0 {
		fmt.Printf("speedup warp vs without: %.2fx\n", without.elapsed.Seconds()/withOnly.elapsed.Seconds())
	}
	if withFull.elapsed > 0 {
		fmt.Printf("speedup warp+extras vs without: %.2fx\n", without.elapsed.Seconds()/withFull.elapsed.Seconds())
	}
}
