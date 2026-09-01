package main

import (
	"context"
	"fmt"

	"github.com/mirkobrombin/go-warp/v2/streambus"
)

func main() {
	ctx := context.Background()
	bus := streambus.NewInMemory(streambus.Config{ReplayCapacity: 16})
	defer bus.Close()

	updates, err := bus.Subscribe(ctx, streambus.SubscribeOptions{
		Topic:    "ui:metrics",
		Buffer:   32,
		Overflow: streambus.LatestOnly,
		Replay:   1,
	})
	if err != nil {
		panic(err)
	}
	defer updates.Close()

	_, err = bus.Publish(ctx, streambus.Frame{
		Topic:       "ui:metrics",
		Payload:     []byte(`{"cpu":42}`),
		Reliability: streambus.Unreliable,
		Priority:    streambus.PriorityInteractive,
	})
	if err != nil {
		panic(err)
	}

	frame := <-updates.Frames()
	fmt.Printf("%s #%d: %s\n", frame.Topic, frame.Sequence, frame.Payload)
}
