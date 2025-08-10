package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/cache/versioned"
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
func (b errBus) RevokeLease(ctx context.Context, id string) error                    { return b.err }
func (b errBus) SubscribeLease(ctx context.Context, id string) (chan struct{}, error) {
	return nil, nil
}
func (b errBus) UnsubscribeLease(ctx context.Context, id string, ch chan struct{}) error {
	return nil
}

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

func (c *ttlCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	return v, ok, nil
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

type mockStrategy struct {
	mu      sync.Mutex
	ttl     time.Duration
	records []string
}

func (m *mockStrategy) Record(key string) {
	m.mu.Lock()
	m.records = append(m.records, key)
	m.mu.Unlock()
}

func (m *mockStrategy) TTL(key string) time.Duration {
	return m.ttl
}

type slowStore[T any] struct {
	data    map[string]T
	delay   time.Duration
	mu      sync.Mutex
	calls   int
	started chan struct{}
	once    sync.Once
}

func (s *slowStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	s.once.Do(func() { close(s.started) })
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	select {
	case <-time.After(s.delay):
		s.mu.Lock()
		v, ok := s.data[key]
		s.mu.Unlock()
		return v, ok, nil
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	}
}

func (s *slowStore[T]) Set(ctx context.Context, key string, value T) error {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()
	return nil
}

func (s *slowStore[T]) Keys(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	return keys, nil
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

func TestWarpGetAtRollback(t *testing.T) {
	ctx := context.Background()
	base := cache.NewInMemory[merge.VersionedValue[string]]()
	c := versioned.New[string](base, 5)
	w := New[string](c, nil, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)

	if err := w.Set(ctx, "foo", "v1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t1 := time.Now()
	time.Sleep(time.Millisecond)
	if err := w.Set(ctx, "foo", "v2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, err := w.GetAt(ctx, "foo", t1)
	if err != nil || v != "v1" {
		t.Fatalf("expected v1, got %v err %v", v, err)
	}
}

func TestWarpGetAtExpire(t *testing.T) {
	ctx := context.Background()
	base := cache.NewInMemory[merge.VersionedValue[string]]()
	c := versioned.New[string](base, 5)
	w := New[string](c, nil, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, 5*time.Millisecond)

	if err := w.Set(ctx, "foo", "v1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := w.GetAt(ctx, "foo", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after ttl, got %v", err)
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

func TestWarpWarmupContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &slowStore[string]{
		data:    map[string]string{"a": "1", "b": "2"},
		delay:   50 * time.Millisecond,
		started: make(chan struct{}),
	}
	w := New[string](cache.NewInMemory[merge.Value[string]](), store, nil, merge.NewEngine[string]())
	w.Register("a", ModeStrongLocal, time.Minute)
	w.Register("b", ModeStrongLocal, time.Minute)

	done := make(chan struct{})
	go func() {
		w.Warmup(ctx)
		close(done)
	}()

	<-store.started
	cancel()
	<-done

	store.mu.Lock()
	calls := store.calls
	store.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 store call, got %d", calls)
	}

	if _, ok, _ := w.cache.Get(context.Background(), "b"); ok {
		t.Fatalf("expected key b not to be warmed up")
	}
}

func TestWarpUnregister(t *testing.T) {
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	if _, ok := w.getReg("foo"); !ok {
		t.Fatalf("expected foo to be registered")
	}
	w.Unregister("foo")
	if _, ok := w.getReg("foo"); ok {
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
	reg, _ := w.getReg("foo")
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

	healed, ok, err := c.Get(ctx, "k")
	if err != nil || !ok || healed.Data != "v1" {
		t.Fatalf("expected value healed to v1, got %v err %v", healed.Data, err)
	}
	if newTTL := c.TTL("k"); newTTL != orig {
		t.Fatalf("expected TTL %v, got %v", orig, newTTL)
	}
}
func TestWarpRegisterDynamicTTL(t *testing.T) {
	ctx := context.Background()
	c := newTTLCache[merge.Value[string]]()
	strat := &mockStrategy{ttl: 2 * time.Second}
	w := New[string](c, nil, nil, merge.NewEngine[string]())
	if !w.RegisterDynamicTTL("foo", ModeStrongLocal, strat) {
		t.Fatalf("expected registration success")
	}
	if err := w.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ttl := c.TTL("foo"); ttl != 2*time.Second {
		t.Fatalf("expected ttl 2s, got %v", ttl)
	}
	if _, err := w.Get(ctx, "foo"); err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	strat.mu.Lock()
	defer strat.mu.Unlock()
	if len(strat.records) != 2 {
		t.Fatalf("expected 2 record calls, got %d", len(strat.records))
	}
}

func TestWarpTxnCASMismatch(t *testing.T) {
	ctx := context.Background()
	w := New[string](cache.NewInMemory[merge.Value[string]](), adapter.NewInMemoryStore[string](), nil, merge.NewEngine[string]())
	w.Register("foo", ModeStrongLocal, time.Minute)
	if err := w.Set(ctx, "foo", "a"); err != nil {
		t.Fatalf("set: %v", err)
	}
	txn := w.Txn(ctx)
	txn.CompareAndSwap("foo", "b", "c")
	if err := txn.Commit(); !errors.Is(err, ErrCASMismatch) {
		t.Fatalf("expected ErrCASMismatch got %v", err)
	}
}

func TestConcurrentRegisterGetSet(t *testing.T) {
	ctx := context.Background()
	w := New[string](cache.NewInMemory[merge.Value[string]](), nil, syncbus.NewInMemoryBus(), merge.NewEngine[string]())
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i)
			if !w.Register(key, ModeStrongLocal, time.Minute) {
				t.Errorf("register failed for %s", key)
				return
			}
			val := fmt.Sprintf("v%d", i)
			if err := w.Set(ctx, key, val); err != nil {
				t.Errorf("set failed for %s: %v", key, err)
				return
			}
			if got, err := w.Get(ctx, key); err != nil || got != val {
				t.Errorf("get failed for %s: %v %v", key, got, err)
			}
		}()
	}
	wg.Wait()
}
