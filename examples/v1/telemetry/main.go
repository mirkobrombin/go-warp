package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	ctx := context.Background()

	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	defer func() { _ = tp.Shutdown(ctx) }()
	otel.SetTracerProvider(tp)

	reg := metrics.NewRegistry()
	metrics.RegisterCoreMetrics(reg)

	c := cache.NewInMemory[merge.Value[string]](cache.WithMetrics[merge.Value[string]](reg))
	w := core.New[string](c, nil, nil, nil, core.WithMetrics[string](reg))

	w.Register("greeting", core.ModeStrongLocal, time.Minute)
	_ = w.Set(ctx, "greeting", "telemetry example")
	_, _ = w.Get(ctx, "greeting")

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	log.Fatal(http.ListenAndServe(":2112", nil))
}
