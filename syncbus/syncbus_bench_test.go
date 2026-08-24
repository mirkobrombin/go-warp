package syncbus

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkPublish_InMemory(b *testing.B) {
	bus := NewInMemoryBus()
	ctx := context.Background()
	key := "bench-key"

	// Setup subscribers
	numSubs := 10
	for i := 0; i < numSubs; i++ {
		_, _ = bus.Subscribe(ctx, key)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bus.Publish(ctx, key)
	}
}

func BenchmarkPublish_Parallel_InMemory(b *testing.B) {
	bus := NewInMemoryBus()
	ctx := context.Background()
	key := "bench-key"

	// Setup subscribers
	numSubs := 10
	for i := 0; i < numSubs; i++ {
		_, _ = bus.Subscribe(ctx, key)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = bus.Publish(ctx, key)
		}
	})
}

func BenchmarkSubscribe_InMemory(b *testing.B) {
	bus := NewInMemoryBus()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, _ = bus.Subscribe(ctx, key)
	}
}
