package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

func TestLeaseGrantAutoRenewAttachRevoke(t *testing.T) {
	ctx := context.Background()
	c := cache.NewInMemory[merge.Value[string]]()
	bus := syncbus.NewInMemoryBus()
	w := New[string](c, nil, bus, merge.NewEngine[string]())
	lm := w.leases

	ttl := 20 * time.Millisecond
	id, err := lm.Grant(ctx, ttl)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	key := "foo"
	lm.Attach(id, key)
	_ = w.cache.Set(ctx, key, merge.Value[string]{Data: "bar", Timestamp: time.Now()}, time.Minute)

	time.Sleep(3 * ttl)
	lm.mu.Lock()
	_, ok := lm.leases[id]
	lm.mu.Unlock()
	if !ok {
		t.Fatalf("lease expired before renewal")
	}

	lm.Revoke(ctx, id)

	lm.mu.Lock()
	if _, ok := lm.leases[id]; ok {
		t.Fatalf("lease not removed after revoke")
	}
	lm.mu.Unlock()

	if _, ok, _ := w.cache.Get(ctx, key); ok {
		t.Fatalf("cache key not invalidated on revoke")
	}
}

func TestLeaseGrantRejectsNonPositiveTTL(t *testing.T) {
	ctx := context.Background()
	c := cache.NewInMemory[merge.Value[string]]()
	w := New[string](c, nil, nil, merge.NewEngine[string]())

	if _, err := w.leases.Grant(ctx, 0); !errors.Is(err, ErrInvalidLeaseTTL) {
		t.Fatalf("expected ErrInvalidLeaseTTL, got %v", err)
	}

	w.leases.mu.Lock()
	if len(w.leases.leases) != 0 {
		t.Fatalf("expected no leases to be created for invalid ttl")
	}
	w.leases.mu.Unlock()
}

func TestLeaseBusRevocation(t *testing.T) {
	ctx := context.Background()
	c := cache.NewInMemory[merge.Value[string]]()
	bus := syncbus.NewInMemoryBus()
	w := New[string](c, nil, bus, merge.NewEngine[string]())
	lm := w.leases

	id, err := lm.Grant(ctx, time.Minute)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	lm.Attach(id, "a")
	_ = w.cache.Set(ctx, "a", merge.Value[string]{Data: "1", Timestamp: time.Now()}, time.Minute)

	ghostID := "ghost"
	lm.Attach(ghostID, "b")
	_ = w.cache.Set(ctx, "b", merge.Value[string]{Data: "2", Timestamp: time.Now()}, time.Minute)

	_ = bus.RevokeLease(ctx, id)
	_ = bus.RevokeLease(ctx, ghostID)

	time.Sleep(20 * time.Millisecond)

	lm.mu.Lock()
	if _, ok := lm.leases[id]; ok {
		lm.mu.Unlock()
		t.Fatalf("lease not revoked via bus")
	}
	if _, ok := lm.leases[ghostID]; ok {
		lm.mu.Unlock()
		t.Fatalf("placeholder lease not revoked via bus")
	}
	lm.mu.Unlock()

	if _, ok, _ := w.cache.Get(ctx, "a"); ok {
		t.Fatalf("key a not invalidated via bus revocation")
	}
	if _, ok, _ := w.cache.Get(ctx, "b"); ok {
		t.Fatalf("key b not invalidated via placeholder revocation")
	}
}
