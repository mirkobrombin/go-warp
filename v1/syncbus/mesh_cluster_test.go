package syncbus_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus/mesh"
)

func TestMeshCluster_Gossip(t *testing.T) {
	nodeCount := 10
	basePort := 8100 // Use different port range to avoid conflicts
	nodes := make([]*core.Warp[string], nodeCount)

	// Prepare peer list
	var allPeers []string
	for i := 0; i < nodeCount; i++ {
		allPeers = append(allPeers, fmt.Sprintf("127.0.0.1:%d", basePort+i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// IMPORTANT: All nodes share the SAME InMemoryStore to simulate L2 consistency
	sharedStore := adapter.NewInMemoryStore[string]()

	var readyWg sync.WaitGroup
	readyWg.Add(nodeCount)

	for i := 0; i < nodeCount; i++ {
		port := basePort + i
		addr := fmt.Sprintf("127.0.0.1:%d", port)

		var peers []string
		for _, p := range allPeers {
			if p != addr {
				peers = append(peers, p)
			}
		}

		opts := mesh.MeshOptions{
			Port:          port,
			Peers:         peers,
			AdvertiseAddr: addr,
			Heartbeat:     200 * time.Millisecond,
		}

		bus, err := mesh.NewMeshBus(opts)
		if err != nil {
			t.Fatalf("node %d bus setup failed: %v", i, err)
		}

		c := cache.NewInMemory[merge.Value[string]]()
		e := merge.NewEngine[string]()
		w := core.New(c, sharedStore, bus, e)
		w.Register("test-key", core.ModeEventualDistributed, time.Hour)

		nodes[i] = w
		readyWg.Done()
	}

	readyWg.Wait()
	time.Sleep(500 * time.Millisecond) // Let mesh settle

	// Step 1: Node 0 sets initial value
	if err := nodes[0].Set(ctx, "test-key", "valueA"); err != nil {
		t.Fatalf("node 0 initial set failed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Step 2: All other nodes Get -> They should get "valueA" from shared Store (and cache it in L1)
	for i := 1; i < nodeCount; i++ {
		val, err := nodes[i].Get(ctx, "test-key")
		if err != nil || val != "valueA" {
			t.Errorf("node %d initial Get: expected valueA, got %v, err %v", i, val, err)
		}
	}

	t.Log("All nodes have valueA in L1. Node 0 updating to valueB...")

	// Step 3: Node 0 updates to "valueB" -> This publishes invalidation
	if err := nodes[0].Set(ctx, "test-key", "valueB"); err != nil {
		t.Fatalf("node 0 update set failed: %v", err)
	}

	// Step 4: Wait for invalidation propagation
	time.Sleep(1 * time.Second)

	// Step 5: All other nodes Get -> They should get "valueB" (L1 invalidated, refetch from Store)
	successCount := 0
	for i := 1; i < nodeCount; i++ {
		val, err := nodes[i].Get(ctx, "test-key")
		if err == nil && val == "valueB" {
			successCount++
		} else if val == "valueA" {
			t.Errorf("Node %d STALE! Still has valueA (invalidation not received)", i)
		} else {
			t.Errorf("Node %d unexpected: val=%v, err=%v", i, val, err)
		}
	}

	if successCount == nodeCount-1 {
		t.Logf("SUCCESS: All %d peer nodes received invalidation and have valueB.", successCount)
	} else {
		t.Fatalf("FAILED: Only %d/%d nodes received invalidation.", successCount, nodeCount-1)
	}
}
