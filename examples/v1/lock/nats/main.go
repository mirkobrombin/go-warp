package main

import (
	"context"
	"fmt"
	"time"

	nats "github.com/nats-io/nats.go"

	"github.com/mirkobrombin/go-warp/v1/lock"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func main() {
	ctx := context.Background()

	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		panic(err)
	}
	defer nc.Close()

	bus := syncbus.NewNATSBus(nc)

	node1 := lock.NewInMemory(bus)
	node2 := lock.NewInMemory(bus)

	go func() {
		if err := node1.Acquire(ctx, "leader", time.Second); err != nil {
			panic(err)
		}
		fmt.Println("node1 elected as leader")
		time.Sleep(500 * time.Millisecond)
		_ = node1.Release(ctx, "leader")
	}()

	time.Sleep(100 * time.Millisecond)

	if err := node2.Acquire(ctx, "leader", time.Second); err != nil {
		panic(err)
	}
	fmt.Println("node2 elected as leader")
	_ = node2.Release(ctx, "leader")
}
