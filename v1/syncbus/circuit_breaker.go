package syncbus

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

type state int

const (
	stateClosed state = iota
	stateOpen
	stateHalfOpen
)

// CircuitBreakerBus decorates a Bus with circuit breaker logic.
type CircuitBreakerBus struct {
	bus       Bus
	mu        sync.RWMutex
	state     state
	failures  int
	threshold int
	timeout   time.Duration
	lastFail  time.Time
}

// NewCircuitBreaker returns a new CircuitBreakerBus.
func NewCircuitBreaker(bus Bus, threshold int, timeout time.Duration) *CircuitBreakerBus {
	return &CircuitBreakerBus{
		bus:       bus,
		threshold: threshold,
		timeout:   timeout,
		state:     stateClosed,
	}
}

// IsHealthy returns true if the circuit is closed.
func (cb *CircuitBreakerBus) IsHealthy() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if cb.state == stateOpen {
		return time.Since(cb.lastFail) > cb.timeout
	}
	return true
}

// allow checks if a request should be allowed.
// It handles the transition from Open to Half-Open based on timeout.
func (cb *CircuitBreakerBus) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateClosed:
		return true
	case stateOpen:
		if time.Since(cb.lastFail) > cb.timeout {
			cb.state = stateHalfOpen
			return true
		}
		return false
	case stateHalfOpen:
		return false // Only allow one probe at a time, strictly managed by logic below
	}
	return false
}

func (cb *CircuitBreakerBus) onSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case stateHalfOpen:
		cb.state = stateClosed
		cb.failures = 0
	case stateClosed:
		cb.failures = 0
	}
}

func (cb *CircuitBreakerBus) onFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.lastFail = time.Now()
	cb.failures++
	if cb.state == stateClosed && cb.failures >= cb.threshold {
		cb.state = stateOpen
	} else if cb.state == stateHalfOpen {
		cb.state = stateOpen
	}
}

// Publish implements Bus.Publish with circuit breaker logic.
func (cb *CircuitBreakerBus) Publish(ctx context.Context, key string, opts ...PublishOption) error {
	if !cb.allow() {
		return ErrCircuitOpen
	}

	err := cb.bus.Publish(ctx, key, opts...)
	if err != nil {
		cb.onFailure()
		return err
	}
	cb.onSuccess()
	return nil
}

// PublishAndAwait implements Bus.PublishAndAwait with circuit breaker logic.
func (cb *CircuitBreakerBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...PublishOption) error {
	if !cb.allow() {
		return ErrCircuitOpen
	}

	err := cb.bus.PublishAndAwait(ctx, key, replicas, opts...)
	if err != nil {
		cb.onFailure()
		return err
	}
	cb.onSuccess()
	return nil
}

// PublishAndAwaitTopology implements Bus.PublishAndAwaitTopology with circuit breaker logic.
func (cb *CircuitBreakerBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...PublishOption) error {
	if !cb.allow() {
		return ErrCircuitOpen
	}

	err := cb.bus.PublishAndAwaitTopology(ctx, key, minZones, opts...)
	if err != nil {
		cb.onFailure()
		return err
	}
	cb.onSuccess()
	return nil
}

// Proxy other methods directly (they might also need protection, but usually Publish is the critical path)

func (cb *CircuitBreakerBus) Subscribe(ctx context.Context, key string) (<-chan Event, error) {
	return cb.bus.Subscribe(ctx, key)
}

func (cb *CircuitBreakerBus) Unsubscribe(ctx context.Context, key string, ch <-chan Event) error {
	return cb.bus.Unsubscribe(ctx, key, ch)
}

func (cb *CircuitBreakerBus) RevokeLease(ctx context.Context, id string) error {
	if !cb.allow() {
		return ErrCircuitOpen
	}
	err := cb.bus.RevokeLease(ctx, id)
	if err != nil {
		cb.onFailure()
		return err
	}
	cb.onSuccess()
	return nil
}

func (cb *CircuitBreakerBus) SubscribeLease(ctx context.Context, id string) (<-chan Event, error) {
	return cb.bus.SubscribeLease(ctx, id)
}

func (cb *CircuitBreakerBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan Event) error {
	return cb.bus.UnsubscribeLease(ctx, id, ch)
}
