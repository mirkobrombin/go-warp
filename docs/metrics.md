# Metrics

The `metrics` package exposes Prometheus counters and gauges for Warp components.
Register the metrics on a Prometheus registry and export them using your preferred
HTTP handler.

```go
reg := metrics.NewRegistry()
metrics.RegisterCoreMetrics(reg)
```
