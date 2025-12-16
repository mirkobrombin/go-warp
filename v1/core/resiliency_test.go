package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/merge"
)

// MockCacheFailing implements cache.Cache and always fails
type MockCacheFailing[T any] struct {
	err error
}

func (m *MockCacheFailing[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	return zero, false, m.err
}

func (m *MockCacheFailing[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	return m.err
}

func (m *MockCacheFailing[T]) Invalidate(ctx context.Context, key string) error {
	return m.err
}

func TestResiliency_GetSupressesError(t *testing.T) {
	// Setup failing cache
	failingCache := &MockCacheFailing[merge.Value[string]]{
		err: errors.New("redis down"),
	}
	
	store := &MockStoreFS[string]{
		data: map[string]string{
			"res-key": "db-value",
		},
	}

	// Create Warp with Resiliency
	w := New[string](failingCache, store, nil, nil, WithCacheResiliency[string]())
	w.Register("res-key", ModeStrongLocal, 10*time.Minute)

	// Get should NOT fail, but treat as miss and fetch from DB
	val, err := w.Get(context.Background(), "res-key")
	if err != nil {
		t.Fatalf("Expected no error with resiliency, got: %v", err)
	}
	if val != "db-value" {
		t.Errorf("Expected value from DB 'db-value', got '%v'", val)
	}
}

func TestResiliency_SetSupressesError(t *testing.T) {
	failingCache := &MockCacheFailing[merge.Value[string]]{
		err: errors.New("redis down"),
	}
	store := &MockStoreFS[string]{data: make(map[string]string)}

	w := New[string](failingCache, store, nil, nil, WithCacheResiliency[string]())
	w.Register("res-key-set", ModeStrongLocal, 10*time.Minute)

	// Set should NOT fail
	err := w.Set(context.Background(), "res-key-set", "value")
	if err != nil {
		t.Fatalf("Expected no error on Set with resiliency, got: %v", err)
	}

	// Verify it reached the store (Set ensures DB consistency even if cache fails)
	if store.data["res-key-set"] != "value" {
		t.Errorf("Expected value to be written to store despite cache failure")
	}
}

func TestResiliency_InvalidateSupressesError(t *testing.T) {
	failingCache := &MockCacheFailing[merge.Value[string]]{
		err: errors.New("redis down"),
	}
	store := &MockStoreFS[string]{data: make(map[string]string)}

	w := New[string](failingCache, store, nil, nil, WithCacheResiliency[string]())
	w.Register("res-key-inv", ModeStrongLocal, 10*time.Minute)

	// Invalidate should NOT fail
	err := w.Invalidate(context.Background(), "res-key-inv")
	if err != nil {
		t.Fatalf("Expected no error on Invalidate with resiliency, got: %v", err)
	}
}

func TestResiliency_DisabledByDefault(t *testing.T) {
	failingCache := &MockCacheFailing[merge.Value[string]]{
		err: errors.New("redis down"),
	}
	store := &MockStoreFS[string]{data: make(map[string]string)}

	// Default Warp (No Resiliency)
	w := New[string](failingCache, store, nil, nil)
	w.Register("res-key-def", ModeStrongLocal, 10*time.Minute)

	// Get SHOULD fail
	_, err := w.Get(context.Background(), "res-key-def")
	if err == nil {
		t.Fatal("Expected error when resiliency is disabled, got nil")
	}
}
