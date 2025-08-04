package adapter

import (
	"context"
	"sync"
)

// Store abstracts the primary storage used as fallback and warmup source.
type Store interface {
	// Get retrieves the value for a key from the storage.
	Get(ctx context.Context, key string) (any, error)
	// Set stores the value for a key into the storage.
	Set(ctx context.Context, key string, value any) error
	// Keys returns the list of keys available in the store. It is used for warmup
	// and by the validator.
	Keys(ctx context.Context) ([]string, error)
}

// InMemoryStore is a simple Store implementation backed by a map.
type InMemoryStore struct {
	mu    sync.RWMutex
	items map[string]any
}

// NewInMemoryStore returns a new InMemoryStore.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{items: make(map[string]any)}
}

// Get implements Store.Get.
func (s *InMemoryStore) Get(ctx context.Context, key string) (any, error) {
	s.mu.RLock()
	v, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	return v, nil
}

// Set implements Store.Set.
func (s *InMemoryStore) Set(ctx context.Context, key string, value any) error {
	s.mu.Lock()
	s.items[key] = value
	s.mu.Unlock()
	return nil
}

// Keys implements Store.Keys.
func (s *InMemoryStore) Keys(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	keys := make([]string, 0, len(s.items))
	for k := range s.items {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	return keys, nil
}
