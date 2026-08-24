package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRegisterCoreMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterCoreMetrics(reg)
	GetCounter.Inc()
	SetCounter.Inc()
	InvalidateCounter.Inc()
	WatcherGauge.Set(5)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(mfs) < 4 {
		t.Fatalf("expected metrics registered")
	}
}

func TestRegisterCoreMetricsDuplicatePanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	RegisterCoreMetrics(reg)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	RegisterCoreMetrics(reg)
}
