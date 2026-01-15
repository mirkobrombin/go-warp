package syncbus

import (
	"context"
	"time"

	"github.com/mirkobrombin/go-foundation/pkg/resiliency"
)

// ErrCircuitOpen is returned when the circuit breaker refuses a request.
var ErrCircuitOpen = resiliency.ErrCircuitOpen

// CircuitBreakerBus decorates a Bus with circuit breaker logic.
type CircuitBreakerBus struct {
	bus     Bus
	breaker *resiliency.CircuitBreaker
}

// NewCircuitBreaker returns a new CircuitBreakerBus.
func NewCircuitBreaker(bus Bus, threshold int, timeout time.Duration) *CircuitBreakerBus {
	return &CircuitBreakerBus{
		bus:     bus,
		breaker: resiliency.NewCircuitBreaker(threshold, timeout),
	}
}

// IsHealthy returns true if the circuit is closed.
func (cb *CircuitBreakerBus) IsHealthy() bool {
	return cb.breaker.State() == resiliency.StateClosed
}

// Publish implements Bus.Publish with circuit breaker logic.
func (cb *CircuitBreakerBus) Publish(ctx context.Context, key string, opts ...PublishOption) error {
	return cb.breaker.Execute(func() error {
		return cb.bus.Publish(ctx, key, opts...)
	})
}

// PublishAndAwait implements Bus.PublishAndAwait with circuit breaker logic.
func (cb *CircuitBreakerBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...PublishOption) error {
	return cb.breaker.Execute(func() error {
		return cb.bus.PublishAndAwait(ctx, key, replicas, opts...)
	})
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology with circuit breaker logic.
func (cb *CircuitBreakerBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...PublishOption) error {
	return cb.breaker.Execute(func() error {
		return cb.bus.PublishAndAwaitTopology(ctx, key, minZones, opts...)
	})
}

func (cb *CircuitBreakerBus) Subscribe(ctx context.Context, key string) (<-chan Event, error) {
	return cb.bus.Subscribe(ctx, key)
}

func (cb *CircuitBreakerBus) Unsubscribe(ctx context.Context, key string, ch <-chan Event) error {
	return cb.bus.Unsubscribe(ctx, key, ch)
}

func (cb *CircuitBreakerBus) RevokeLease(ctx context.Context, id string) error {
	return cb.breaker.Execute(func() error {
		return cb.bus.RevokeLease(ctx, id)
	})
}

func (cb *CircuitBreakerBus) SubscribeLease(ctx context.Context, id string) (<-chan Event, error) {
	return cb.bus.SubscribeLease(ctx, id)
}

func (cb *CircuitBreakerBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan Event) error {
	return cb.bus.UnsubscribeLease(ctx, id, ch)
}
