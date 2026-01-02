package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/presets"
	"github.com/mirkobrombin/go-warp/v1/syncbus/mesh"
	redis "github.com/redis/go-redis/v9"
)

var (
	concurrency = flag.Int("c", 50, "Concurrency")
	requests    = flag.Int("n", 100000, "Requests")
	dataSize    = flag.Int("d", 256, "Payload size")
	target      = flag.String("target", "all", "Target: warp-local, warp-redis, ristretto, redis, dragonfly")
	redisAddr   = flag.String("redis-addr", "localhost:6379", "Redis Address")
	dfAddr      = flag.String("df-addr", "localhost:6380", "DragonFly Address")
)

func main() {
	flag.Parse()

	payload := make([]byte, *dataSize)
	for i := range payload {
		payload[i] = 'x'
	}

	targets := strings.Split(*target, ",")
	if *target == "all" {
		targets = []string{"warp-local", "warp-mesh", "ristretto", "warp-redis", "redis", "dragonfly"}
	}

	fmt.Printf("| %-15s | %-10s | %-12s | %-12s |\n", "System", "Ops/sec", "Avg Latency", "P99 Latency")
	fmt.Println("|:---|:---|:---|:---|")

	for _, t := range targets {
		runBenchmark(strings.TrimSpace(t), payload)
	}
}

func runBenchmark(name string, payload []byte) {
	var (
		getFn   func(ctx context.Context, key string) error
		setFn   func(ctx context.Context, key string, val []byte) error
		cleanup func()
	)

	ctx := context.Background()
	key := "bench:key"

	switch name {
	case "warp-local":
		w := presets.NewInMemoryStandalone[[]byte]()
		w.Register(key, core.ModeStrongLocal, time.Hour)
		setFn = func(ctx context.Context, k string, v []byte) error { return w.Set(ctx, k, v) }
		getFn = func(ctx context.Context, k string) error { _, err := w.Get(ctx, k); return err }

	case "warp-mesh":
		w := presets.NewMeshEventual[[]byte](mesh.MeshOptions{
			Port:      7946,
			Heartbeat: 1 * time.Hour,
		})
		w.Register(key, core.ModeEventualDistributed, time.Hour)
		setFn = func(ctx context.Context, k string, v []byte) error { return w.Set(ctx, k, v) }
		getFn = func(ctx context.Context, k string) error { _, err := w.Get(ctx, k); return err }

	case "ristretto":
		cache, _ := ristretto.NewCache(&ristretto.Config{
			NumCounters: 1e7,
			MaxCost:     1 << 30,
			BufferItems: 64,
		})
		setFn = func(ctx context.Context, k string, v []byte) error {
			cache.Set(k, v, 1)
			return nil
		}
		getFn = func(ctx context.Context, k string) error {
			_, found := cache.Get(k)
			if !found {
				return fmt.Errorf("not found")
			}
			return nil
		}
		cleanup = func() { cache.Close() }

	case "warp-redis":
		w := presets.NewRedisEventual[[]byte](presets.RedisOptions{Addr: *redisAddr})
		w.Register(key, core.ModeEventualDistributed, time.Hour)
		setFn = func(ctx context.Context, k string, v []byte) error { return w.Set(ctx, k, v) }
		getFn = func(ctx context.Context, k string) error { _, err := w.Get(ctx, k); return err }

	case "redis":
		r := redis.NewClient(&redis.Options{Addr: *redisAddr})
		setFn = func(ctx context.Context, k string, v []byte) error { return r.Set(ctx, k, v, 0).Err() }
		getFn = func(ctx context.Context, k string) error { return r.Get(ctx, k).Err() }
		cleanup = func() { r.Close() }

	case "dragonfly":
		r := redis.NewClient(&redis.Options{Addr: *dfAddr})
		setFn = func(ctx context.Context, k string, v []byte) error { return r.Set(ctx, k, v, 0).Err() }
		getFn = func(ctx context.Context, k string) error { return r.Get(ctx, k).Err() }
		cleanup = func() { r.Close() }

	default:
		log.Printf("Unknown target: %s", name)
		return
	}

	if cleanup != nil {
		defer cleanup()
	}

	// Warmup
	if err := setFn(ctx, key, payload); err != nil {
		// Log but continue, might be connection refused if container is missing
		// fmt.Printf("| %-15s | %-10s | %-12s | %-12s |\n", name, "FAIL", "-", "-")
		// return
	}

	var wg sync.WaitGroup
	var ops int64
	totalReqs := *requests
	latencies := make([]int64, totalReqs)

	start := time.Now()
	chunk := totalReqs / *concurrency

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			offset := idx * chunk
			for j := 0; j < chunk; j++ {
				reqStart := time.Now()
				if err := getFn(ctx, key); err == nil {
					atomic.AddInt64(&ops, 1)
					latencies[offset+j] = time.Since(reqStart).Nanoseconds()
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	if ops == 0 {
		fmt.Printf("| %-15s | %-10s | %-12s | %-12s |\n", name, "ERROR", "-", "-")
		return
	}

	throughput := float64(ops) / elapsed.Seconds()
	avgLat := float64(elapsed.Nanoseconds()) / float64(ops)

	// Calculate P99
	var p99 string = "-"
	validLats := make([]int64, 0, ops)
	for _, l := range latencies {
		if l > 0 {
			validLats = append(validLats, l)
		}
	}
	if len(validLats) > 0 {
		sort.Slice(validLats, func(i, j int) bool { return validLats[i] < validLats[j] })
		p99Idx := int(float64(len(validLats)) * 0.99)
		if p99Idx >= len(validLats) {
			p99Idx = len(validLats) - 1
		}
		p99 = fmt.Sprintf("%d", validLats[p99Idx])
	}

	fmt.Printf("| %-15s | %-10.0f | %-12.0f | %-12s |\n", name, throughput, avgLat, p99)
}
