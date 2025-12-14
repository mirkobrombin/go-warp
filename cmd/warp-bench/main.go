package main

import (
	"context"
	"flag"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/presets"
)

var (
	concurrency = flag.Int("c", 50, "Number of concurrent clients")
	requests    = flag.Int("n", 100000, "Total number of requests")
	dataSize    = flag.Int("d", 256, "Data size in bytes")
)

func main() {
	flag.Parse()

	log.Printf("Starting benchmark: %d requests, %d concurrency, %d bytes payload", *requests, *concurrency, *dataSize)

	log.Println("Initializing Warp (InMemory Standalone)...")
	w := presets.NewInMemoryStandalone[[]byte]()

	ctx := context.Background()
	key := "bench_key"
	val := make([]byte, *dataSize)
	for i := 0; i < *dataSize; i++ {
		val[i] = 'x'
	}

	w.Register(key, core.ModeStrongLocal, time.Hour)
	if err := w.Set(ctx, key, val); err != nil {
		log.Fatalf("Setup failed: %v", err)
	}

	var wg sync.WaitGroup
	var ops int64
	var errorsCount int64

	start := time.Now()

	totalReqs := *requests
	reqsPerWorker := totalReqs / *concurrency

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < reqsPerWorker; j++ {
				_, err := w.Get(ctx, key)
				if err != nil {
					atomic.AddInt64(&errorsCount, 1)
				}
				atomic.AddInt64(&ops, 1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	throughput := float64(ops) / elapsed.Seconds()
	avgLatency := elapsed.Seconds() / float64(ops) * 1e9 // ns

	log.Printf("Finished in %v", elapsed)
	log.Printf("Throughput: %.2f req/s", throughput)
	log.Printf("Avg Latency: %.2f ns", avgLatency)
	if errorsCount > 0 {
		log.Printf("Errors: %d", errorsCount)
	}
}
