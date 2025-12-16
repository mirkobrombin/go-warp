package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

// MockStoreFS implements adapter.Store for testing FailSafe
type MockStoreFS[T any] struct {
	data    map[string]T
	fail    bool
	failErr error
}

func (m *MockStoreFS[T]) Get(ctx context.Context, key string) (T, bool, error) {
	if m.fail {
		var zero T
		return zero, false, m.failErr
	}
	val, ok := m.data[key]
	return val, ok, nil
}

func (m *MockStoreFS[T]) Set(ctx context.Context, key string, value T) error {
	if m.fail {
		return m.failErr
	}
	m.data[key] = value
	return nil
}

func (m *MockStoreFS[T]) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *MockStoreFS[T]) Keys(ctx context.Context) ([]string, error) {
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestFailSafe_ReturnsStaleOnError(t *testing.T) {
	// Setup
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	store := &MockStoreFS[string]{
		data:    make(map[string]string),
		failErr: errors.New("db boom"),
	}
	w := New[string](c, store, nil, nil)

	key := "fs-key-1"
	val := "initial-value"
	store.data[key] = val

	// Register with short TTL (50ms) and long Grace Period (1h)
	ttl := 50 * time.Millisecond
	w.Register(key, ModeStrongLocal, ttl, cache.WithFailSafe(1*time.Hour))

	// 1. Initial Get (Populate Cache)
	got, err := w.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Initial Get failed: %v", err)
	}
	if got != val {
		t.Errorf("Initial Get = %v, want %v", got, val)
	}

	// 2. Wait for TTL to expire (50ms)
	time.Sleep(100 * time.Millisecond)

	// 3. Make Store Fail
	store.fail = true

	// 4. Get again -> Should return Stale Data (Fail-Safe)
	got, err = w.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Expected stale value but got error: %v", err)
	}
	if got != val {
		t.Errorf("Expected stale value %v, got %v", val, got)
	}
}

func TestFailSafe_RecoversWhenStoreRecover(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	store := &MockStoreFS[string]{
		data: make(map[string]string),
	}
	w := New[string](c, store, nil, nil)

	key := "fs-key-2"
	val1 := "value-1"
	store.data[key] = val1

	w.Register(key, ModeStrongLocal, 50*time.Millisecond, cache.WithFailSafe(1*time.Hour))

	// 1. Populate
	w.Get(context.Background(), key)

	// 2. Expire
	time.Sleep(100 * time.Millisecond)

	// 3. Update Store + Fail
	val2 := "value-2"
	store.data[key] = val2
	store.fail = true
	store.failErr = errors.New("temp error")

	// 4. Get -> Stale "value-1"
	got, err := w.Get(context.Background(), key)
	if err != nil || got != val1 {
		t.Errorf("Expected stale 'value-1', got %v (err: %v)", got, err)
	}

	// 5. Recover Store
	store.fail = false

	// 6. Get -> Fresh "value-2"
	got, err = w.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Post-recovery Get failed: %v", err)
	}
	if got != val2 {
		t.Errorf("Expected fresh 'value-2' after recovery, got %v", got)
	}
}

func TestFailSafe_RespectsGracePeriod(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	store := &MockStoreFS[string]{
		data:    make(map[string]string),
		failErr: errors.New("db boom"),
	}
	w := New[string](c, store, nil, nil)

	key := "fs-key-3"
	store.data[key] = "val"

	// Register with short TTL (20ms) and SHORT Grace Period (50ms)
	// Total life = 70ms
	w.Register(key, ModeStrongLocal, 20*time.Millisecond, cache.WithFailSafe(50*time.Millisecond))

	// 1. Populate
	w.Get(context.Background(), key)

	// 2. Wait for TTL + Grace Period to expire (Wait 150ms > 70ms)
	time.Sleep(150 * time.Millisecond)

	// 3. Make Store Fail
	store.fail = true

	// 4. Get -> Should Fail (Item evicted)
	_, err := w.Get(context.Background(), key)
	if err == nil {
		t.Fatal("Expected error because grace period expired, but got success")
	}
}

func TestFailSafe_ReturnsErrorIfNotFound(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	store := &MockStoreFS[string]{
		data: make(map[string]string),
	}
	w := New[string](c, store, nil, nil)

	key := "fs-key-4"
	store.data[key] = "val"

	w.Register(key, ModeStrongLocal, 50*time.Millisecond, cache.WithFailSafe(1*time.Hour))
	w.Get(context.Background(), key)
	
	time.Sleep(100 * time.Millisecond)

	// Simulate deletion from DB (Get returns !ok or ErrNotFound)
	delete(store.data, key)
	
	// Get -> Should return ErrNotFound (or zero/false), NOT stale data.
	_, err := w.Get(context.Background(), key)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Expected ErrNotFound when DB deletes item, got: %v", err)
	}
}
