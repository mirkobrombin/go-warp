package syncbus_test

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	busredis "github.com/mirkobrombin/go-warp/v1/syncbus/redis"
	redis "github.com/redis/go-redis/v9"
)

func TestRedisCluster_Gossip(t *testing.T) {
	redisAddr := "localhost:6379"

	nodeCount := 10
	nodes := make([]*core.Warp[string], nodeCount)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track resources for cleanup
	var buses []*busredis.RedisBus
	var clients []*redis.Client

	defer func() {
		for _, b := range buses {
			_ = b.Close()
		}
		for _, c := range clients {
			_ = c.Close()
		}
	}()

	// All nodes connect to the same Redis (shared Store and Bus)
	for i := 0; i < nodeCount; i++ {
		client := redis.NewClient(&redis.Options{Addr: redisAddr})
		if err := client.Ping(ctx).Err(); err != nil {
			t.Skipf("Skipping Redis test, connection failed: %v", err)
			return
		}
		clients = append(clients, client)

		bus := busredis.NewRedisBus(busredis.RedisBusOptions{Client: client})
		buses = append(buses, bus)

		c := cache.NewInMemory[merge.Value[string]]()
		s := adapter.NewRedisStore[string](client) // Shared Redis Store
		e := merge.NewEngine[string]()

		w := core.New(c, s, bus, e)
		w.Register("test-key", core.ModeEventualDistributed, time.Hour)
		nodes[i] = w
	}

	// Clean up previous test data
	client := redis.NewClient(&redis.Options{Addr: redisAddr})
	_ = client.Del(ctx, "test-key").Err()

	// Wait for all nodes to fully subscribe before testing
	time.Sleep(2 * time.Second)

	// Step 1: Node 0 sets initial value
	if err := nodes[0].Set(ctx, "test-key", "valueA"); err != nil {
		t.Fatalf("node 0 initial set failed: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Step 2: All other nodes Get -> They should get "valueA"
	for i := 1; i < nodeCount; i++ {
		val, err := nodes[i].Get(ctx, "test-key")
		if err != nil || val != "valueA" {
			t.Errorf("node %d initial Get: expected valueA, got %v, err %v", i, val, err)
		}
	}

	t.Log("All Redis nodes have valueA in L1. Node 0 updating to valueB...")

	// Step 3: Node 0 updates to "valueB"
	if err := nodes[0].Set(ctx, "test-key", "valueB"); err != nil {
		t.Fatalf("node 0 update set failed: %v", err)
	}

	// Step 4: Wait for invalidation propagation
	time.Sleep(2 * time.Second)

	// Step 5: Verify
	successCount := 0
	for i := 1; i < nodeCount; i++ {
		val, err := nodes[i].Get(ctx, "test-key")
		if val == "valueB" {
			successCount++
		} else if val == "valueA" {
			t.Errorf("Node %d STALE! Still has valueA", i)
		} else {
			t.Errorf("Node %d unexpected: val=%q err=%v", i, val, err)
		}
	}

	if successCount == nodeCount-1 {
		t.Logf("SUCCESS: All %d Redis peer nodes received invalidation and have valueB.", successCount)
	} else {
		t.Fatalf("FAILED: Only %d/%d Redis nodes received invalidation.", successCount, nodeCount-1)
	}
}
