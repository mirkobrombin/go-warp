package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

// MockStoreER implements adapter.Store and helps track eager refreshes
type MockStoreER[T any] struct {
	mu          sync.Mutex
	data        map[string]T
	delay       time.Duration
	refreshCall int
}

func (m *MockStoreER[T]) Get(ctx context.Context, key string) (T, bool, error) {
	m.mu.Lock()
	m.refreshCall++
	m.mu.Unlock()

	select {
	case <-time.After(m.delay):
		val, ok := m.data[key]
		return val, ok, nil
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	}
}

func (m *MockStoreER[T]) Set(ctx context.Context, key string, value T) error {
	m.mu.Lock()
	m.data[key] = value
	m.mu.Unlock()
	return nil
}

func (m *MockStoreER[T]) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	delete(m.data, key)
	m.mu.Unlock()
	return nil
}

func (m *MockStoreER[T]) Keys(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestEagerRefresh_TriggersBackgroundUpdate(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))

	// Store takes 100ms to respond
	store := &MockStoreER[string]{
		data:  make(map[string]string),
		delay: 100 * time.Millisecond,
	}
	w := New[string](c, store, nil, nil)

	key := "er-key-1"
	val1 := "value-1"
	val2 := "value-2"
	store.data[key] = val1

	// Register with:
	// TTL: 200ms
	// EagerRefreshThreshold: 0.5 (Refresh when 50% of TTL (100ms) is remaining)
	w.Register(key, ModeStrongLocal, 200*time.Millisecond,
		cache.WithEagerRefresh(0.5),
	)

	// 1. Initial Populate (Will be slow, 100ms, as it's a miss)
	start := time.Now()
	got, err := w.Get(context.Background(), key)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Initial Get failed: %v", err)
	}
	if got != val1 {
		t.Errorf("Initial Get = %v, want %v", got, val1)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("Initial Get was too fast (%v), expected > 100ms", elapsed)
	}
	if store.refreshCall != 1 {
		t.Errorf("Expected 1 store call, got %d", store.refreshCall)
	}

	// 2. Wait until just before Eager Refresh threshold (e.g., 90ms elapsed)
	// TTL = 200ms. 50% is 100ms remaining. So trigger point is after 100ms.
	// Current timestamp for item is now + 100ms from start of test roughly.
	// So, we want current time to be (creationTime + 100ms).
	// Let's sleep 90ms from the last Get finish (which took 100ms).
	time.Sleep(90 * time.Millisecond) // Total elapsed: 100ms (fetch) + 90ms (sleep) = 190ms.
	// Remaining: 200ms - 190ms = 10ms. 10/200 = 0.05. Below 0.5 threshold.

	// Change backend data to verify refresh
	store.data[key] = val2
	store.mu.Lock()
	store.refreshCall = 0 // Reset counter for next phase
	store.mu.Unlock()

	// 3. Get Again (Should trigger Eager Refresh)
	// This call should return value-1 immediately (from cache)
	start = time.Now()
	got, err = w.Get(context.Background(), key)
	elapsed = time.Since(start)
	if err != nil {
		t.Fatalf("Second Get failed: %v", err)
	}
	if got != val1 {
		t.Errorf("Second Get = %v, want %v (stale during refresh)", got, val1)
	}
	if elapsed > 10*time.Millisecond { // Should be fast, from cache
		t.Errorf("Second Get took too long (%v), expected fast", elapsed)
	}

	// 4. Wait for background refresh to complete
	time.Sleep(150 * time.Millisecond) // Enough for store.delay (100ms) + some buffer

	// 5. Get one more time (Should get fresh data from eager refresh)
	got, err = w.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Third Get failed: %v", err)
	}
	if got != val2 {
		t.Errorf("Third Get = %v, want %v (fresh after eager refresh)", got, val2)
	}
	if store.refreshCall != 1 {
		t.Errorf("Expected 1 store call for eager refresh, got %d", store.refreshCall)
	}
}

func TestEagerRefresh_DoesNotTriggerIfTooFresh(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))

	store := &MockStoreER[string]{
		data:  make(map[string]string),
		delay: 10 * time.Millisecond,
	}
	w := New[string](c, store, nil, nil)

	key := "er-key-2"
	store.data[key] = "val-fresh"

	w.Register(key, ModeStrongLocal, 100*time.Millisecond,
		cache.WithEagerRefresh(0.2), // Refresh when 20% TTL (20ms) is remaining
	)

	// 1. Initial Populate
	w.Get(context.Background(), key)
	store.mu.Lock()
	if store.refreshCall != 1 {
		t.Errorf("Expected 1 store call, got %d", store.refreshCall)
	}
	store.refreshCall = 0
	store.mu.Unlock()

	// 2. Get again immediately (should not trigger eager refresh)
	w.Get(context.Background(), key)
	store.mu.Lock()
	if store.refreshCall != 0 {
		t.Errorf("Expected 0 store calls for eager refresh (too fresh), got %d", store.refreshCall)
	}
	store.mu.Unlock()

	// 3. Wait until after eager refresh threshold but before TTL
	// Total TTL 100ms. Eager refresh at 80ms elapsed.
	time.Sleep(85 * time.Millisecond) // Item should be 85ms old, remaining 15ms. 15/100 = 0.15, less than 0.2 threshold.

	// 4. Get again (should trigger eager refresh)
	w.Get(context.Background(), key)
	time.Sleep(20 * time.Millisecond) // Give background goroutine time to complete
	store.mu.Lock()
	if store.refreshCall != 1 {
		t.Errorf("Expected 1 store call for eager refresh (now within window), got %d", store.refreshCall)
	}
	store.mu.Unlock()
}

func TestEagerRefresh_DoesNotTriggerIfStoreNil(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))

	// No store provided
	w := New[string](c, nil, nil, nil)

	key := "er-key-3"
	val := "val-no-store"

	// Manually set into cache as there is no store to fetch from
	now := time.Now()
	mv := merge.Value[string]{Data: val, Timestamp: now}
	_ = c.Set(context.Background(), key, mv, 100*time.Millisecond)

	w.Register(key, ModeStrongLocal, 100*time.Millisecond,
		cache.WithEagerRefresh(0.1), // Refresh when 10% TTL (10ms) is remaining
	)

	// Wait for eager refresh window
	time.Sleep(95 * time.Millisecond)

	// Get (should not panic or error, should not try to refresh from nil store)
	got, err := w.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get with nil store failed: %v", err)
	}
	if got != val {
		t.Errorf("Expected %v, got %v", val, got)
	}
}
