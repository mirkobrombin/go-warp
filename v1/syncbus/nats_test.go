package syncbus

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats-server/v2/server"
	natsserver "github.com/nats-io/nats-server/v2/test"
	nats "github.com/nats-io/nats.go"
)

func newNATSBus(t *testing.T) (*NATSBus, context.Context) {
	t.Helper()
	addr := os.Getenv("WARP_TEST_NATS_ADDR")
	forceReal := os.Getenv("WARP_TEST_FORCE_REAL") == "true"

	if forceReal && addr == "" {
		t.Fatal("WARP_TEST_FORCE_REAL is true but WARP_TEST_NATS_ADDR is empty")
	}

	var conn *nats.Conn
	var s *server.Server
	var err error

	if addr != "" {
		t.Logf("TestNATSBus: using real NATS at %s", addr)
		conn, err = nats.Connect(addr)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
	} else {
		t.Log("TestNATSBus: using embedded NATS server")
		s = natsserver.RunRandClientPortServer()
		conn, err = nats.Connect(s.ClientURL())
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
	}

	bus := NewNATSBus(conn)
	ctx := context.Background()
	t.Cleanup(func() {
		conn.Close()
		if s != nil {
			s.Shutdown()
		}
	})
	return bus, ctx
}

func TestNATSBusPublishSubscribeFlowAndMetrics(t *testing.T) {
	bus, ctx := newNATSBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := bus.Publish(ctx, "key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected published 1 got %d", metrics.Published)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected delivered 1 got %d", metrics.Delivered)
	}
}

func TestNATSBusContextBasedUnsubscribe(t *testing.T) {
	bus, _ := newNATSBus(t)
	subCtx, cancel := context.WithCancel(context.Background())
	ch, err := bus.Subscribe(subCtx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for unsubscribe")
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, ok := bus.subs["key"]; ok {
		t.Fatal("subscription still present after context cancel")
	}
}

func TestNATSBusDeduplicatePendingKeys(t *testing.T) {
	bus, ctx := newNATSBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	bus.mu.Lock()
	bus.pending["key"] = struct{}{}
	bus.mu.Unlock()
	if err := bus.Publish(ctx, "key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ch:
		t.Fatal("unexpected publish when key pending")
	default:
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
	if metrics.Delivered != 0 {
		t.Fatalf("expected delivered 0 got %d", metrics.Delivered)
	}
}

func TestNATSBusReconnectAfterClose(t *testing.T) {
	bus, ctx := newNATSBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	bus.conn.Close()
	if err := bus.Publish(ctx, "key"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
	metrics := bus.Metrics()
	if metrics.Published != 1 {
		t.Fatalf("expected published 1 got %d", metrics.Published)
	}
	if metrics.Delivered != 1 {
		t.Fatalf("expected delivered 1 got %d", metrics.Delivered)
	}
}

func TestNATSBusReconnectLoop(t *testing.T) {
	bus, ctx := newNATSBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	bus.conn.Close()
	done := make(chan error, 1)
	go func() { done <- bus.Publish(ctx, "key") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publish timeout")
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
}

func TestNATSBusIdempotentAfterReconnect(t *testing.T) {
	bus, ctx := newNATSBus(t)
	ch, err := bus.Subscribe(ctx, "key")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	id := uuid.NewString()
	if err := bus.conn.Publish("key", []byte(id)); err != nil {
		t.Fatalf("direct publish: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for publish")
	}
	bus.conn.Close()
	if err := bus.reconnect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if err := bus.conn.Publish("key", []byte(id)); err != nil {
		t.Fatalf("dup publish: %v", err)
	}
	select {
	case <-ch:
		t.Fatal("duplicate delivered")
	case <-time.After(200 * time.Millisecond):
	}
}
