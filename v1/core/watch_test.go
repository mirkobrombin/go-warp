package core

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/metrics"
	"github.com/mirkobrombin/go-warp/v1/watchbus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestWatchPrefixMetricsAndEvents(t *testing.T) {
	bus := watchbus.NewInMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	metrics.WatcherGauge.Set(0)

	ch, err := WatchPrefix(ctx, bus, "foo")
	if err != nil {
		t.Fatalf("watch prefix: %v", err)
	}
	if v := testutil.ToFloat64(metrics.WatcherGauge); v != 1 {
		t.Fatalf("expected gauge 1 got %v", v)
	}

	if err := bus.Publish(ctx, "foobar", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-ch:
		if string(msg) != "hello" {
			t.Fatalf("unexpected message %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	cancel()
	time.Sleep(20 * time.Millisecond)
	if v := testutil.ToFloat64(metrics.WatcherGauge); v != 0 {
		t.Fatalf("expected gauge 0 got %v", v)
	}
}
