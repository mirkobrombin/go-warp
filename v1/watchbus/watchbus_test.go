package watchbus

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryWatchBus(t *testing.T) {
	bus := NewInMemory()
	ctx := context.Background()
	ch, err := bus.Watch(ctx, "foo")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if err := bus.Publish(ctx, "foo", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-ch:
		if string(msg) != "hello" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
	if err := bus.Unwatch(ctx, "foo", ch); err != nil {
		t.Fatalf("unwatch: %v", err)
	}
}

func TestInMemoryWatchBusPrefix(t *testing.T) {
	bus := NewInMemory()
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
		t.Fatal("timeout waiting for key msg")
	}
	select {
	case msg := <-chPrefix:
		if string(msg) != "a" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for prefix msg")
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
		t.Fatal("timeout waiting for key msg")
	}
	select {
	case msg := <-chPrefix:
		if string(msg) != "b" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for prefix msg")
	}
	_ = bus.Unwatch(ctx, "foo1", chKey)
	_ = bus.Unwatch(ctx, "foo", chPrefix)
}

func TestInMemoryWatchBusContextCanceled(t *testing.T) {
	bus := NewInMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bus.Watch(ctx, "k"); err == nil {
		t.Fatal("expected watch error on canceled context")
	}
	if err := bus.Publish(ctx, "k", nil); err == nil {
		t.Fatal("expected publish error on canceled context")
	}
	ch := make(chan []byte)
	if err := bus.Unwatch(ctx, "k", ch); err == nil {
		t.Fatal("expected unwatch error on canceled context")
	}
}

func TestInMemoryWatchBusUnwatchUnknown(t *testing.T) {
	bus := NewInMemory()
	ctx := context.Background()
	ch := make(chan []byte)
	// Should not panic or error when channel not registered
	if err := bus.Unwatch(ctx, "missing", ch); err != nil {
		t.Fatalf("unwatch unexpected error: %v", err)
	}
}
