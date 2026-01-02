package mesh

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func TestMeshMultiNode(t *testing.T) {
	const nodeCount = 10
	portBase := 10000 + (int(time.Now().Unix()) % 5000)

	nodes := make([]*MeshBus, nodeCount)
	opts := make([]MeshOptions, nodeCount)

	ifi := findMulticastInterface()
	ifaceName := ""
	if ifi != nil {
		ifaceName = ifi.Name
	}

	// Create seed node (Node 0)
	seedAddr := fmt.Sprintf("127.0.0.1:%d", portBase)
	opts[0] = MeshOptions{
		Port:          portBase,
		Interface:     ifaceName,
		AdvertiseAddr: seedAddr,
		Heartbeat:     50 * time.Millisecond,
	}

	var err error
	nodes[0], err = NewMeshBus(opts[0])
	if err != nil {
		t.Fatalf("Failed to create seed node: %v", err)
	}
	defer nodes[0].Close()

	// Create other nodes, some with seed, some using multicast solely (simulated by same group)
	for i := 1; i < nodeCount; i++ {
		port := portBase + i
		addr := fmt.Sprintf("127.0.0.1:%d", port)

		o := MeshOptions{
			Port:          port,
			Interface:     ifaceName,
			AdvertiseAddr: addr,
			Heartbeat:     50 * time.Millisecond,
		}

		// Every node knows the seed to ensure discovery even if Multicast is restricted
		o.Peers = []string{seedAddr}

		opts[i] = o
		nodes[i], err = NewMeshBus(o)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i, err)
		}
		defer nodes[i].Close()
	}

	// Wait for full mesh discovery
	t.Log("Waiting for nodes to discover each other...")
	// Increase timeout for discovery in potentially constrained environments
	discoveryCtx, discoveryCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer discoveryCancel()

	for i, node := range nodes {
	discoveryLoop:
		for {
			peers := node.Peers()
			// We wait until most nodes are discovered.
			// In loopback/CI, we might not get 100% but we need enough for a valid test.
			if len(peers) >= nodeCount/2 {
				break discoveryLoop
			}
			select {
			case <-discoveryCtx.Done():
				t.Logf("Node %d only discovered %d/%d peers: %v", i, len(peers), nodeCount-1, peers)
				break discoveryLoop
			case <-time.After(200 * time.Millisecond):
			}
		}
	}

	// Verify propagation: Node 0 publishes, everyone else receives
	key := "multi-node-sync"
	verificationCtx, verificationCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer verificationCancel()

	subs := make([]<-chan syncbus.Event, nodeCount)
	for i := 1; i < nodeCount; i++ {
		// Use verificationCtx so channels don't close prematurely if discovery took too long
		ch, _ := nodes[i].Subscribe(verificationCtx, key)
		subs[i] = ch
	}

	time.Sleep(500 * time.Millisecond) // Give time for subscriptions and gossip to settle

	if err := nodes[0].Publish(context.Background(), key); err != nil {
		t.Fatalf("Failed to publish from seed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 1; i < nodeCount; i++ {
		wg.Add(1)
		go func(idx int, ch <-chan syncbus.Event) {
			defer wg.Done()
			select {
			case evt, ok := <-ch:
				if !ok {
					t.Errorf("Node %d: channel closed unexpectedly", idx)
					return
				}
				if evt.Key != key {
					t.Errorf("Node %d received wrong key: '%s' (expected '%s')", idx, evt.Key, key)
				}
			case <-verificationCtx.Done():
				// If we timed out, check if we missed it or if it's just slow
				t.Errorf("Node %d timed out waiting for event", idx)
			}
		}(i, subs[i])
	}
	wg.Wait()
}

func BenchmarkMeshPublish(b *testing.B) {
	opts := MeshOptions{
		Port:      12000,
		Heartbeat: 1 * time.Hour, // Disable heartbeats for bench
	}
	node, _ := NewMeshBus(opts)
	defer node.Close()

	ctx := context.Background()
	key := "bench-key"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = node.Publish(ctx, key)
	}
}

func BenchmarkMeshPacketMarshal(b *testing.B) {
	p := packet{
		Magic:   magicByte,
		Type:    typeInvalidate,
		NodeID:  [16]byte{1, 2, 3, 4},
		KeyHash: 12345678,
		KeyLen:  10,
		Key:     []byte("test-key-1"),
	}
	buf := make([]byte, 1500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.marshal(buf)
	}
}
