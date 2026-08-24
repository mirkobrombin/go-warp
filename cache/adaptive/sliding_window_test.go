package adaptive

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSlidingWindowAdjustsTTL(t *testing.T) {
	reg := prometheus.NewRegistry()
	sw := NewSlidingWindow(50*time.Millisecond, 2, time.Second, 5*time.Second, reg)
	key := "k"

	sw.Record(key)
	if ttl := sw.TTL(key); ttl != time.Second {
		t.Fatalf("expected cold ttl, got %v", ttl)
	}
	if c := testutil.ToFloat64(sw.adjustCounter); c != 1 {
		t.Fatalf("expected 1 adjustment, got %v", c)
	}
	if g := testutil.ToFloat64(sw.hotGauge); g != 0 {
		t.Fatalf("expected 0 hot keys, got %v", g)
	}

	sw.Record(key)
	sw.Record(key)
	if ttl := sw.TTL(key); ttl != 5*time.Second {
		t.Fatalf("expected hot ttl, got %v", ttl)
	}
	if c := testutil.ToFloat64(sw.adjustCounter); c != 2 {
		t.Fatalf("expected 2 adjustments, got %v", c)
	}
	if g := testutil.ToFloat64(sw.hotGauge); g != 1 {
		t.Fatalf("expected 1 hot key, got %v", g)
	}

	time.Sleep(80 * time.Millisecond)
	sw.Record(key)
	if ttl := sw.TTL(key); ttl != time.Second {
		t.Fatalf("expected cold ttl after window, got %v", ttl)
	}
	if c := testutil.ToFloat64(sw.adjustCounter); c != 3 {
		t.Fatalf("expected 3 adjustments, got %v", c)
	}
	if g := testutil.ToFloat64(sw.hotGauge); g != 0 {
		t.Fatalf("expected 0 hot keys after cooling, got %v", g)
	}
}
