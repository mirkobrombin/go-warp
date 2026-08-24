package watchbus

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/test"
	nats "github.com/nats-io/nats.go"
)

func TestNATSWatchBus(t *testing.T) {
	s := natsserver.RunRandClientPortServer()
	defer s.Shutdown()

	conn, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	bus := NewNATSWatchBus(conn)
	ctx := context.Background()

	// foo.1 uses NATS dot-separated subjects so that the prefix wildcard foo.> matches.
	chKey, err := bus.Watch(ctx, "foo.1")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	chPrefix, err := bus.SubscribePrefix(ctx, "foo")
	if err != nil {
		t.Fatalf("sub prefix: %v", err)
	}

	if err := bus.Publish(ctx, "foo.1", []byte("a")); err != nil {
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
	// prefix subscriber receives via NATS wildcard routing
	select {
	case msg := <-chPrefix:
		if string(msg) != "b" {
			t.Fatalf("unexpected %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for prefix message from publish prefix")
	}

	if err := bus.Unwatch(ctx, "foo.1", chKey); err != nil {
		t.Fatalf("unwatch: %v", err)
	}
	if err := bus.Unwatch(ctx, "foo", chPrefix); err != nil {
		t.Fatalf("unwatch prefix: %v", err)
	}

	bus.mu.Lock()
	_, hasKey := bus.watches["foo.1"]
	_, hasPrefix := bus.prefixWatches["foo"]
	bus.mu.Unlock()
	if hasKey {
		t.Fatal("expected key removed from watches")
	}
	if hasPrefix {
		t.Fatal("expected prefix removed from prefixWatches")
	}
}

func TestNATSWatchBusContextCancellation(t *testing.T) {
	s := natsserver.RunRandClientPortServer()
	defer s.Shutdown()

	conn, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	bus := NewNATSWatchBus(conn)
	watchCtx, cancel := context.WithCancel(context.Background())

	ch, err := bus.Watch(watchCtx, "bar.1")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed after context cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}

	bus.mu.Lock()
	_, hasKey := bus.watches["bar.1"]
	bus.mu.Unlock()
	if hasKey {
		t.Fatal("expected key removed from watches after context cancel")
	}
}
