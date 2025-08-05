package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	"github.com/mirkobrombin/go-warp/v1/validator"
)

type errStore[T any] struct {
	err error
}

func (s errStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	return zero, false, s.err
}

func (s errStore[T]) Set(ctx context.Context, key string, value T) error { return nil }

func (s errStore[T]) Keys(ctx context.Context) ([]string, error) { return nil, nil }

type errBus struct{ err error }

func (b errBus) Publish(ctx context.Context, key string) error                       { return b.err }
func (b errBus) Subscribe(ctx context.Context, key string) (chan struct{}, error)    { return nil, nil }
func (b errBus) Unsubscribe(ctx context.Context, key string, ch chan struct{}) error { return nil }

type ttlCache[T any] struct {
	mu    sync.Mutex
	items map[string]T
	ttls  map[string]time.Duration
}

func newTTLCache[T any]() *ttlCache[T] {
	return &ttlCache[T]{
		items: make(map[string]T),
		ttls:  make(map[string]time.Duration),
	}
}

func (c *ttlCache[T]) Get(ctx context.Context, key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	return v, ok
}

func (c *ttlCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = value
	c.ttls[key] = ttl
	return nil
}

func (c *ttlCache[T]) Invalidate(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
	delete(c.ttls, key)
	return nil
}

func (c *ttlCache[T]) TTL(key string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ttls[key]
}

func TestWarpSetGet(t *testing.T) {
	ctx := context.Background()
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, syncbus.NewInMemoryBus(), merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, err := w.Get(ctx, "foo")
	if err != nil || v != "bar" {
		t.Fatalf("unexpected value: %v, err: %v", v, err)
	}
	if err := w.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := w.Get(ctx, "foo"); err == nil {
		t.Fatalf("expected error after invalidate")
	}
}

func TestWarpMerge(t *testing.T) {
	ctx := context.Background()
	w := New[int](cache.NewInMemory[merge.Value[int]](), adapter.NewInMemoryStore[int](), nil, merge.NewEngine[int]())
	w.Register("cnt", ModeStrongLocal, time.Minute)
	w.Merge("cnt", func(old, new int) (int, error) {
		return old + new, nil
	})
	if err := w.Set(ctx, "cnt", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := w.Set(ctx, "cnt", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, err := w.Get(ctx, "cnt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 3 {
		t.Fatalf("expected 3, got %v", v)
	}
}

func TestWarpFallbackAndWarmup(t *testing.T) {
	ctx := context.Background()
	store := adapter.NewInMemoryStore[string]()
	_ = store.Set(ctx, "foo", "bar")
	w := New[string](cache.NewInMemory[merge.Value[string]](), store, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	// fallback
	v, err := w.Get(ctx, "foo")
	if err != nil || v != "bar" {
		t.Fatalf("unexpected fallback value: %v err: %v", v, err)
	}
	// warmup
	if err := w.Invalidate(ctx, "foo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	w.Warmup(ctx)
	v, err = w.Get(ctx, "foo")
	if err != nil || v != "bar" {
		t.Fatalf("expected warmup to load value, got %v err %v", v, err)
	}
}

func TestWarpUnregister(t *testing.T) {
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	if _, ok := w.regs["foo"]; !ok {
		t.Fatalf("expected foo to be registered")
	}
	w.Unregister("foo")
	if _, ok := w.regs["foo"]; ok {
		t.Fatalf("expected foo to be unregistered")
	}
}

func TestWarpGetStoreError(t *testing.T) {
	ctx := context.Background()
	expected := errors.New("boom")
	w := New[string](cache.NewInMemory[merge.Value[string]](), errStore[string]{err: expected}, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	if _, err := w.Get(ctx, "foo"); !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestWarpRegisterDuplicate(t *testing.T) {
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, nil, merge.NewEngine[string]())
	if !w.Register("foo", ModeStrongLocal, time.Minute) {
		t.Fatalf("expected first registration to succeed")
	}
	if w.Register("foo", ModeEventualDistributed, 2*time.Minute) {
		t.Fatalf("expected duplicate registration to fail")
	}
	reg := w.regs["foo"]
	if reg.mode != ModeStrongLocal || reg.ttl != time.Minute {
		t.Fatalf("registration should not be overwritten")
	}
}

func TestWarpSetPublishError(t *testing.T) {
	ctx := context.Background()
	expected := errors.New("boom")
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, errBus{err: expected}, merge.NewEngine[string]())
	w.Register("foo", ModeEventualDistributed, time.Minute)
	if err := w.Set(ctx, "foo", "bar"); !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestWarpInvalidatePublishError(t *testing.T) {
	ctx := context.Background()
	expected := errors.New("boom")
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, errBus{err: expected}, merge.NewEngine[string]())
	w.Register("foo", ModeEventualDistributed, time.Minute)
	if err := w.Invalidate(ctx, "foo"); !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestWarpUnregisteredKey(t *testing.T) {
	ctx := context.Background()
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, nil, merge.NewEngine[string]())

	if _, err := w.Get(ctx, "foo"); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("expected ErrUnregistered, got %v", err)
	}

	if err := w.Set(ctx, "foo", "bar"); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("expected ErrUnregistered, got %v", err)
	}

	if err := w.Invalidate(ctx, "foo"); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("expected ErrUnregistered, got %v", err)
	}
}

func TestWarpValidatorAutoHealTTL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newTTLCache[merge.Value[string]]()
	store := adapter.NewInMemoryStore[string]()
	_ = store.Set(ctx, "k", "v1")

	w := New[string](c, store, nil, merge.NewEngine[string]())
	ttl := time.Minute
	w.Register("k", ModeStrongLocal, ttl)

	mv := merge.Value[string]{Data: "v0", Timestamp: time.Now()}
	_ = c.Set(ctx, "k", mv, ttl)
	orig := c.TTL("k")

	v := w.Validator(validator.ModeAutoHeal, time.Millisecond)
	go v.Run(ctx)
	time.Sleep(5 * time.Millisecond)

	healed, ok := c.Get(ctx, "k")
	if !ok || healed.Data != "v1" {
		t.Fatalf("expected value healed to v1, got %v", healed.Data)
	}
	if newTTL := c.TTL("k"); newTTL != orig {
		t.Fatalf("expected TTL %v, got %v", orig, newTTL)
	}
}
