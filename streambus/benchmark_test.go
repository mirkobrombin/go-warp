package streambus

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkPublishFanout(b *testing.B) {
	for _, subscribers := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("subscribers-%d", subscribers), func(b *testing.B) {
			bus := NewInMemory(Config{DefaultBuffer: 1024, MaxBuffer: 1024})
			defer bus.Close()
			for i := 0; i < subscribers; i++ {
				subscription, err := bus.Subscribe(context.Background(), SubscribeOptions{
					Topic: "benchmark", Buffer: 1024, Overflow: LatestOnly,
				})
				if err != nil {
					b.Fatal(err)
				}
				defer subscription.Close()
				go func() {
					for range subscription.Frames() {
					}
				}()
			}
			frame := Frame{Topic: "benchmark", Payload: []byte("payload")}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := bus.Publish(context.Background(), frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
