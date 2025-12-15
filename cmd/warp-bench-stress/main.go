package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

var (
	allocGB  = flag.Int("alloc-gb", 10, "Target memory allocation in GB")
	duration = flag.Duration("duration", 5*time.Minute, "Duration of the stress test")
	procs    = flag.Int("procs", 8, "Number of concurrent goroutines")
)

// LargeObject simulates a heavy payload
type LargeObject struct {
	Data [1024]byte // 1KB
}

func main() {
	flag.Parse()

	go func() {
		log.Println("Starting pprof on :6060")
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	targetBytes := uint64(*allocGB) * 1024 * 1024 * 1024
	approxEntrySize := uint64(1200)
	numEntries := int(targetBytes / approxEntrySize)

	log.Printf("Targeting %d GB (~%d entries)", *allocGB, numEntries)

	c := cache.NewInMemory[merge.Value[LargeObject]](
		cache.WithMaxEntries[merge.Value[LargeObject]](numEntries),
	)
	w := core.New[LargeObject](c, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	log.Println("Phase 1: Filling Cache...")
	fillStart := time.Now()
	var wg sync.WaitGroup
	chunkSize := numEntries / *procs

	for p := 0; p < *procs; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start := id * chunkSize
			end := start + chunkSize
			for i := start; i < end; i++ {
				key := fmt.Sprintf("key-%d", i)
				// Must register key first!
				w.Register(key, core.ModeStrongLocal, 1*time.Hour)
				if err := w.Set(context.Background(), key, LargeObject{}); err != nil {
					log.Printf("Set error: %v", err)
				}
				if i%100000 == 0 {
					runtime.Gosched()
				}
			}
		}(p)
	}
	wg.Wait()
	log.Printf("Filled in %v. Current Memory:", time.Since(fillStart))
	printMemStats()

	log.Printf("Phase 2: Churing (%v)...", *duration)

	for p := 0; p < *procs; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

			for {
				select {
				case <-ctx.Done():
					return
				default:
					k := r.Intn(numEntries)
					key := fmt.Sprintf("key-%d", k)

					if r.Float32() < 0.2 {
						w.Set(context.Background(), key, LargeObject{})
					} else {
						w.Get(context.Background(), key)
					}
				}
			}
		}(p)
	}

	monitorTicker := time.NewTicker(5 * time.Second)
	defer monitorTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-monitorTicker.C:
				printMemStats()
			}
		}
	}()

	wg.Wait()
	log.Println("Stress Test Completed.")
}

func printMemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("Alloc = %v MiB", m.Alloc/1024/1024)
	fmt.Printf("\tTotalAlloc = %v MiB", m.TotalAlloc/1024/1024)
	fmt.Printf("\tSys = %v MiB", m.Sys/1024/1024)
	fmt.Printf("\tNumGC = %v\n", m.NumGC)
}
