package mesh

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func findMulticastInterface() *net.Interface {
	ifaces, _ := net.Interfaces()
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagMulticast != 0 && ifi.Flags&net.FlagUp != 0 && ifi.Flags&net.FlagLoopback == 0 {
			return &ifi
		}
	}
	return nil
}

func TestMeshIntegration(t *testing.T) {
	ifi := findMulticastInterface()
	ifaceName := ""
	if ifi != nil {
		ifaceName = ifi.Name
	}

	opts := MeshOptions{
		Port:      8000 + (int(time.Now().Unix()) % 1000), // Randomish port
		Group:     "239.0.0.1",
		Interface: ifaceName,
	}

	nodeA, err := NewMeshBus(opts)
	if err != nil {
		t.Fatalf("Failed to create nodeA: %v", err)
	}
	defer nodeA.Close()

	nodeB, err := NewMeshBus(opts)
	if err != nil {
		t.Fatalf("Failed to create nodeB: %v", err)
	}
	defer nodeB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	key := "test-key"
	chB, err := nodeB.Subscribe(ctx, key)
	if err != nil {
		t.Fatalf("Failed to subscribe on nodeB: %v", err)
	}

	// Wait a bit for subscription to be ready
	time.Sleep(200 * time.Millisecond)

	if err := nodeA.Publish(ctx, key); err != nil {
		t.Fatalf("Failed to publish from nodeA: %v", err)
	}

	select {
	case evt := <-chB:
		if evt.Key != key {
			t.Errorf("Expected key %s, got %s", key, evt.Key)
		}
	case <-ctx.Done():
		t.Error("Timed out waiting for invalidation on nodeB")
	}
}

func TestMeshLoopback(t *testing.T) {
	ifi := findMulticastInterface()
	ifaceName := ""
	if ifi != nil {
		ifaceName = ifi.Name
	}

	opts := MeshOptions{
		Port:      8000 + (int(time.Now().Unix()) % 1000) + 1,
		Group:     "239.0.0.1",
		Interface: ifaceName,
	}

	node, err := NewMeshBus(opts)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	defer node.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	key := "loop-key"
	ch, err := node.Subscribe(ctx, key)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	if err := node.Publish(ctx, key); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	select {
	case <-ch:
		t.Error("Node received its own message (loopback should be filtered)")
	case <-ctx.Done():
		// Success
	}
}

func TestMeshGossip(t *testing.T) {
	portA := 9000 + (int(time.Now().Unix()) % 100)
	portB := portA + 1

	addrA := fmt.Sprintf("127.0.0.1:%d", portA)

	optsA := MeshOptions{
		Port:          portA,
		AdvertiseAddr: addrA,
		Heartbeat:     100 * time.Millisecond,
	}
	nodeA, err := NewMeshBus(optsA)
	if err != nil {
		t.Fatalf("Failed to create nodeA: %v", err)
	}
	defer nodeA.Close()

	optsB := MeshOptions{
		Port:          portB,
		AdvertiseAddr: fmt.Sprintf("127.0.0.1:%d", portB),
		Peers:         []string{addrA},
		Heartbeat:     100 * time.Millisecond,
	}
	nodeB, err := NewMeshBus(optsB)
	if err != nil {
		t.Fatalf("Failed to create nodeB: %v", err)
	}
	defer nodeB.Close()

	// Wait for gossip discovery
	found := false
	for i := 0; i < 20; i++ {
		peers := nodeA.Peers()
		for _, p := range peers {
			if p == optsB.AdvertiseAddr {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Errorf("NodeA did not discover NodeB via gossip (Peers: %v)", nodeA.Peers())
	}
}
