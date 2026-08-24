package watchbus

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// BenchmarkInMemoryPublish measures publish throughput with many concurrent
// publishers and watchers.
func BenchmarkInMemoryPublish(b *testing.B) {
	bus := NewInMemory()
	ctx := context.Background()

	const watchers = 1000
	for i := 0; i < watchers; i++ {
		key := fmt.Sprintf("key-%d", i)
		ch, _ := bus.Watch(ctx, key)
		go func(c chan []byte) {
			for range c {
			}
		}(ch)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(0))
		for pb.Next() {
			key := fmt.Sprintf("key-%d", r.Intn(watchers))
			_ = bus.Publish(ctx, key, []byte("data"))
		}
	})
}
