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
