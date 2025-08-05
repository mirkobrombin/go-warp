package validator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	digester   Digester
}

// New creates a new Validator.
func New(c cache.Cache, s adapter.Store, mode Mode, interval time.Duration) *Validator {
	return &Validator{
		cache:    c,
		store:    s,
		mode:     mode,
		interval: interval,
		digester: JSONDigester{},
	}
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
		cvDigest, err := v.digester.Digest(cv)
		if err != nil {
			continue
		}
		svDigest, err := v.digester.Digest(sv)
		if err != nil {
			continue
		}
		if cvDigest != svDigest {
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

// SetDigester sets the digester used for value comparison.
func (v *Validator) SetDigester(d Digester) {
	if d != nil {
		v.digester = d
	}
}

// Digester provides value serialization and hashing.
type Digester interface {
	Digest(v any) (string, error)
}

// JSONDigester serializes values using JSON and hashes them with SHA256.
type JSONDigester struct{}

func (JSONDigester) Digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
