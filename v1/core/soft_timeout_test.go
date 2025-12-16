package core

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

// MockStoreSlow implements adapter.Store and simulates latency
type MockStoreSlow[T any] struct {
	data  map[string]T
	delay time.Duration
}

func (m *MockStoreSlow[T]) Get(ctx context.Context, key string) (T, bool, error) {
	select {
	case <-time.After(m.delay):
		val, ok := m.data[key]
		return val, ok, nil
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	}
}

func (m *MockStoreSlow[T]) Set(ctx context.Context, key string, value T) error {
	m.data[key] = value
	return nil
}

func (m *MockStoreSlow[T]) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *MockStoreSlow[T]) Keys(ctx context.Context) ([]string, error) {
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestSoftTimeout(t *testing.T) {
	// Setup
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	
	// Store takes 200ms to respond
	store := &MockStoreSlow[string]{
		data:  make(map[string]string),
		delay: 200 * time.Millisecond,
	}
	
	w := New[string](c, store, nil, nil)

	key := "slow-key"
	val := "fast-value"
	store.data[key] = val

	// Register with:
	// TTL: 20ms (Short, so it expires quickly)
	// FailSafe: 1h (So we have a stale backup)
	// SoftTimeout: 50ms (Much shorter than store delay)
	w.Register(key, ModeStrongLocal, 20*time.Millisecond, 
		cache.WithFailSafe(1*time.Hour),
		cache.WithSoftTimeout(50*time.Millisecond),
	)

	// 1. Initial Populate (Will be slow, 200ms, because no stale data yet)
	start := time.Now()
	got, err := w.Get(context.Background(), key)
	elapsed := time.Since(start)
	
	if err != nil {
		t.Fatalf("Initial Get failed: %v", err)
	}
	if got != val {
		t.Errorf("Initial Get = %v, want %v", got, val)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("Initial Get was too fast (%v), expected > 200ms", elapsed)
	}

	// 2. Wait for TTL to expire (20ms)
	time.Sleep(50 * time.Millisecond)

	// 3. Get Again (Stale exists + Store is slow)
	// Should hit SoftTimeout at 50ms and return stale data immediately
	start = time.Now()
	got, err = w.Get(context.Background(), key)
	elapsed = time.Since(start)

	if err != nil {
		t.Fatalf("Soft Timeout Get failed: %v", err)
	}
	if got != val {
		t.Errorf("Soft Timeout Get = %v, want %v", got, val)
	}
	
	// Check timing: Should be around 50ms (SoftTimeout), definitely NOT 200ms
	if elapsed > 150*time.Millisecond {
		t.Errorf("Soft Timeout Get took too long (%v), expected ~50ms", elapsed)
	}
}
