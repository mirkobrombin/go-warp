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
