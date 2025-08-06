package metrics

import "github.com/prometheus/client_golang/prometheus"

// NewRegistry creates a new Prometheus registry.
func NewRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}
