package syncbus_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	busredis "github.com/mirkobrombin/go-warp/v1/syncbus/redis"
	redis "github.com/redis/go-redis/v9"
)

func TestRedisCluster_Gossip(t *testing.T) {
	redisAddr := "localhost:6379"
	testKey := "test-key-" + uuid.NewString()

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
		w.Register(testKey, core.ModeEventualDistributed, time.Hour)
		nodes[i] = w
	}

	// Clean up previous test data using the first client
	_ = clients[0].Del(ctx, testKey).Err()

	// Wait for all nodes to fully subscribe before testing
	time.Sleep(2 * time.Second)

	// Step 1: Node 0 sets initial value
	if err := nodes[0].Set(ctx, testKey, "valueA"); err != nil {
		t.Fatalf("node 0 initial set failed: %v", err)
	}

	// Verify the value is in Redis L2 before proceeding
	val, err := clients[0].Get(ctx, testKey).Result()
	if err != nil {
		t.Fatalf("failed to verify initial value in Redis: %v", err)
	}
	t.Logf("Verified: %s is set in Redis L2 (raw value exists, len=%d)", testKey, len(val))

	time.Sleep(300 * time.Millisecond)

	// Step 2: All other nodes Get -> They should get "valueA"
	for i := 1; i < nodeCount; i++ {
		val, err := nodes[i].Get(ctx, testKey)
		if err != nil || val != "valueA" {
			t.Errorf("node %d initial Get: expected valueA, got %v, err %v", i, val, err)
		}
	}

	t.Log("All Redis nodes have valueA in L1. Node 0 updating to valueB...")

	// Step 3: Node 0 updates to "valueB"
	if err := nodes[0].Set(ctx, testKey, "valueB"); err != nil {
		t.Fatalf("node 0 update set failed: %v", err)
	}

	// Verify the updated value is in Redis L2
	val, err = clients[0].Get(ctx, testKey).Result()
	if err != nil {
		t.Fatalf("failed to verify updated value in Redis: %v", err)
	}
	t.Logf("Verified: %s updated in Redis L2 (raw value exists, len=%d)", testKey, len(val))

	// Step 4: Wait for invalidation propagation
	time.Sleep(2 * time.Second)

	// Step 5: Verify
	successCount := 0
	for i := 1; i < nodeCount; i++ {
		val, err := nodes[i].Get(ctx, testKey)
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
