package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func sequentialWarmup(w *Warp[string], ctx context.Context) {
	if w.store == nil {
		return
	}
	keys := make([]string, 0)
	for i := 0; i < regShardCount; i++ {
		shard := &w.shards[i]
		shard.RLock()
		for k := range shard.regs {
			keys = append(keys, k)
		}
		shard.RUnlock()
	}
	for _, k := range keys {
		select {
		case <-ctx.Done():
			return
		default:
		}
		v, ok, err := w.store.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		shard := w.shard(k)
		shard.RLock()
		reg := shard.regs[k]
		shard.RUnlock()
		now := time.Now()
		mv := merge.Value[string]{Data: v, Timestamp: now}
		ttl := reg.ttl
		if reg.ttlStrategy != nil {
			ttl = reg.ttlStrategy.TTL(k)
		}
		reg.currentTTL = ttl
		reg.lastAccess = now
		_ = w.cache.Set(ctx, k, mv, ttl)
	}
}

func BenchmarkWarmup(b *testing.B) {
	const numKeys = 500
	ctx := context.Background()
	store := adapter.NewInMemoryStore[string]()
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		k := fmt.Sprintf("key-%d", i)
		keys[i] = k
		_ = store.Set(ctx, k, fmt.Sprintf("val-%d", i))
	}

	b.Run("sequential", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cache := cache.NewInMemory[merge.Value[string]]()
			bus := syncbus.NewInMemoryBus()
			w := New[string](cache, store, bus, nil)
			for _, k := range keys {
				w.Register(k, ModeStrongLocal, time.Minute)
			}
			b.StartTimer()
			sequentialWarmup(w, ctx)
			b.StopTimer()
		}
	})

	b.Run("parallel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cache := cache.NewInMemory[merge.Value[string]]()
			bus := syncbus.NewInMemoryBus()
			w := New[string](cache, store, bus, nil)
			for _, k := range keys {
				w.Register(k, ModeStrongLocal, time.Minute)
			}
			b.StartTimer()
			w.Warmup(ctx)
			b.StopTimer()
		}
	})
}
