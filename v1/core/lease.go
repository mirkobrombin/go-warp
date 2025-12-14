package core

import (
	"context"
	"errors"
	"sync"
	"time"

	uuid "github.com/hashicorp/go-uuid"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

// ErrInvalidLeaseTTL is returned when a non-positive TTL is provided.
var ErrInvalidLeaseTTL = errors.New("warp: lease ttl must be positive")

type lease struct {
	keys   map[string]struct{}
	timer  *time.Timer
	ticker *time.Ticker
	stop   chan struct{}
	sub    <-chan syncbus.Event
}

// LeaseManager manages active leases and their renewal.
type LeaseManager[T any] struct {
	w   *Warp[T]
	bus syncbus.Bus

	mu     sync.Mutex
	leases map[string]*lease
}

func newLeaseManager[T any](w *Warp[T], bus syncbus.Bus) *LeaseManager[T] {
	return &LeaseManager[T]{w: w, bus: bus, leases: make(map[string]*lease)}
}

// Grant creates a new lease with the specified TTL and starts automatic renewal.
func (lm *LeaseManager[T]) Grant(ctx context.Context, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", ErrInvalidLeaseTTL
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		return "", err
	}
	l := &lease{
		keys: make(map[string]struct{}),
		stop: make(chan struct{}),
	}
	// expiration timer
	l.timer = time.AfterFunc(ttl, func() {
		lm.revoke(context.Background(), id, true)
	})
	// periodic renewal
	interval := ttl / 2
	if interval <= 0 {
		interval = ttl
	}
	l.ticker = time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-l.ticker.C:
				l.timer.Reset(ttl)
			case <-l.stop:
				return
			}
		}
	}()
	// subscribe to revocations
	if lm.bus != nil {
		ch, err := lm.bus.SubscribeLease(ctx, id)
		if err == nil {
			l.sub = ch
			go func() {
				select {
				case <-ch:
					lm.revoke(context.Background(), id, false)
				case <-l.stop:
				}
			}()
		}
	}
	lm.mu.Lock()
	lm.leases[id] = l
	lm.mu.Unlock()
	return id, nil
}

// Attach adds a key to an existing lease, creating the lease placeholder if needed.
func (lm *LeaseManager[T]) Attach(id, key string) {
	lm.mu.Lock()
	l, ok := lm.leases[id]
	if !ok {
		l = &lease{keys: make(map[string]struct{}), stop: make(chan struct{})}
		// subscribe to revocations for placeholder
		if lm.bus != nil {
			if ch, err := lm.bus.SubscribeLease(context.Background(), id); err == nil {
				l.sub = ch
				go func() {
					select {
					case <-ch:
						lm.revoke(context.Background(), id, false)
					case <-l.stop:
					}
				}()
			}
		}
		lm.leases[id] = l
	}
	l.keys[key] = struct{}{}
	lm.mu.Unlock()
}

// Revoke stops a lease and optionally propagates the revocation.
func (lm *LeaseManager[T]) Revoke(ctx context.Context, id string) {
	lm.revoke(ctx, id, true)
}

func (lm *LeaseManager[T]) revoke(ctx context.Context, id string, publish bool) {
	lm.mu.Lock()
	l, ok := lm.leases[id]
	if !ok {
		lm.mu.Unlock()
		return
	}
	delete(lm.leases, id)
	lm.mu.Unlock()

	if l.timer != nil {
		l.timer.Stop()
	}
	if l.ticker != nil {
		l.ticker.Stop()
	}
	close(l.stop)
	if lm.bus != nil && l.sub != nil {
		_ = lm.bus.UnsubscribeLease(context.Background(), id, l.sub)
	}
	for key := range l.keys {
		_ = lm.w.cache.Invalidate(ctx, key)
	}
	if publish && lm.bus != nil {
		_ = lm.bus.RevokeLease(ctx, id)
	}
}
