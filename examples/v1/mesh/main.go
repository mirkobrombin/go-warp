package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/presets"
	"github.com/mirkobrombin/go-warp/v1/syncbus/mesh"
)

func main() {
	ctx := context.Background()

	// Node 1 setup
	w1 := presets.NewMeshEventual[string](mesh.MeshOptions{
		Port:          7946,
		AdvertiseAddr: "127.0.0.1:7946",
		Peers:         []string{"127.0.0.1:7947"},
	})

	// Node 2 setup
	w2 := presets.NewMeshEventual[string](mesh.MeshOptions{
		Port:          7947,
		AdvertiseAddr: "127.0.0.1:7947",
		Peers:         []string{"127.0.0.1:7946"},
	})

	// Register key on both nodes.
	// Thanks to the new automatic subscription logic,
	// nodes will now automatically listen for invalidations on the bus.
	w1.Register("shared-key", core.ModeEventualDistributed, time.Minute)
	w2.Register("shared-key", core.ModeEventualDistributed, time.Minute)

	// Node 1 sets a value
	fmt.Println("Node 1: Setting 'shared-key' to 'WarpSpeed'...")
	if err := w1.Set(ctx, "shared-key", "WarpSpeed"); err != nil {
		panic(err)
	}

	// Wait for gossip/propagation
	time.Sleep(500 * time.Millisecond)

	// Node 2 gets the value
	val, err := w2.Get(ctx, "shared-key")
	if err != nil {
		fmt.Printf("Node 2: Error getting key: %v\n", err)
	} else {
		fmt.Printf("Node 2: Successfully retrieved value: %s\n", val)
	}

	// Node 2 updates the value
	fmt.Println("Node 2: Updating 'shared-key' to 'HyperDrive'...")
	if err := w2.Set(ctx, "shared-key", "HyperDrive"); err != nil {
		panic(err)
	}

	// Wait for gossip
	time.Sleep(500 * time.Millisecond)

	// Node 1 gets the updated value
	val1, _ := w1.Get(ctx, "shared-key")
	fmt.Printf("Node 1: Current value: %s\n", val1)
}
