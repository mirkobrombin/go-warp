package watchbus

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisWatchBus(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	bus := NewRedisWatchBus(client)
	ctx := context.Background()

	chKey, err := bus.Watch(ctx, "foo1")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	chPrefix, err := bus.SubscribePrefix(ctx, "foo")
	if err != nil {
		t.Fatalf("sub prefix: %v", err)
	}

	if err := bus.Publish(ctx, "foo1", []byte("a")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-chKey:
		if string(msg) != "a" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for key message")
	}
	select {
	case msg := <-chPrefix:
		if string(msg) != "a" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for prefix message")
	}

	member, err := client.SIsMember(ctx, "watchbus:index", "foo1").Result()
	if err != nil {
		t.Fatalf("sismember: %v", err)
	}
	if !member {
		t.Fatalf("expected key in index")
	}

	if err := bus.PublishPrefix(ctx, "foo", []byte("b")); err != nil {
		t.Fatalf("publish prefix: %v", err)
	}
	select {
	case msg := <-chKey:
		if string(msg) != "b" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for key prefix message")
	}
	// prefix subscriber may receive multiple messages; consume at least one
	select {
	case msg := <-chPrefix:
		if string(msg) != "b" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for prefix message from publish prefix")
	}

	if err := bus.Unwatch(ctx, "foo1", chKey); err != nil {
		t.Fatalf("unwatch: %v", err)
	}
	if err := bus.Unwatch(ctx, "foo", chPrefix); err != nil {
		t.Fatalf("unwatch prefix: %v", err)
	}

	member, err = client.SIsMember(ctx, "watchbus:index", "foo1").Result()
	if err != nil {
		t.Fatalf("sismember: %v", err)
	}
	if member {
		t.Fatalf("expected key removed from index")
	}
}
