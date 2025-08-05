package validator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
)

// Mode defines validator behaviour.
type Mode int

const (
	ModeNoop Mode = iota
	ModeAlert
	ModeAutoHeal
)

// Validator periodically compares cache and storage values.
type Validator struct {
	cache      cache.Cache
	store      adapter.Store
	mode       Mode
	interval   time.Duration
	mismatches uint64
}

// New creates a new Validator.
func New(c cache.Cache, s adapter.Store, mode Mode, interval time.Duration) *Validator {
	return &Validator{cache: c, store: s, mode: mode, interval: interval}
}

// Run starts the validation loop.
func (v *Validator) Run(ctx context.Context) {
	if v.store == nil {
		return
	}
	ticker := time.NewTicker(v.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.scan(ctx)
		}
	}
}

func (v *Validator) scan(ctx context.Context) {
	keys, err := v.store.Keys(ctx)
	if err != nil {
		return
	}
	for _, k := range keys {
		cv, ok := v.cache.Get(ctx, k)
		if !ok {
			continue
		}
		sv, err := v.store.Get(ctx, k)
		if err != nil || sv == nil {
			continue
		}
		if digest(cv) != digest(sv) {
			atomic.AddUint64(&v.mismatches, 1)
			if v.mode == ModeAutoHeal {
				_ = v.cache.Set(ctx, k, sv, 0)
			}
		}
	}
}

// Metrics returns number of mismatches detected.
func (v *Validator) Metrics() uint64 {
	return atomic.LoadUint64(&v.mismatches)
}

func digest(v any) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%v", v)))
	return hex.EncodeToString(h[:])
}
