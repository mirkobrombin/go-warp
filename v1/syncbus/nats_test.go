package syncbus

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/test"
	nats "github.com/nats-io/nats.go"
)

func newNATSBus(t *testing.T) (*NATSBus, context.Context) {
	t.Helper()
	s := natsserver.RunRandClientPortServer()
	conn, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	bus := NewNATSBus(conn)
	ctx := context.Background()
	t.Cleanup(func() {
		conn.Close()
		s.Shutdown()
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

func TestNATSBusPublishError(t *testing.T) {
	bus, ctx := newNATSBus(t)
	bus.conn.Close()
	if err := bus.Publish(ctx, "key"); err == nil {
		t.Fatal("expected publish error")
	}
	metrics := bus.Metrics()
	if metrics.Published != 0 {
		t.Fatalf("expected published 0 got %d", metrics.Published)
	}
}
