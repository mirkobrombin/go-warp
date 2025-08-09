package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// GetCounter tracks the number of Get operations.
	GetCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "warp_get_total",
		Help: "Total number of Get operations",
	})
	// SetCounter tracks the number of Set operations.
	SetCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "warp_set_total",
		Help: "Total number of Set operations",
	})
	// InvalidateCounter tracks the number of invalidations.
	InvalidateCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "warp_invalidate_total",
		Help: "Total number of cache invalidations",
	})
	// WatcherGauge reports the number of active watchers.
	WatcherGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "warp_watchers",
		Help: "Current number of active watchers",
	})
)

// NewRegistry creates a new Prometheus registry.
func NewRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// RegisterCoreMetrics registers warp core metrics on the provided registry.
func RegisterCoreMetrics(reg prometheus.Registerer) {
	reg.MustRegister(GetCounter, SetCounter, InvalidateCounter, WatcherGauge)
}
