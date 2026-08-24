package adapter

import (
	"context"
	"sync"
)

// Store abstracts the primary storage used as fallback and warmup source.
//
// T represents the type of values stored in the adapter.
type Store[T any] interface {
	// Get retrieves the value for a key from the storage.
	// The boolean return indicates whether the key was found.
	Get(ctx context.Context, key string) (T, bool, error)
	// Set stores the value for a key into the storage.
	Set(ctx context.Context, key string, value T) error
	// Keys returns the list of keys available in the store. It is used for warmup
	// and by the validator.
	Keys(ctx context.Context) ([]string, error)
}

// Batch allows grouping multiple operations before committing them to the
// underlying storage.
type Batch[T any] interface {
	Set(ctx context.Context, key string, value T) error
	Delete(ctx context.Context, key string) error
	Commit(ctx context.Context) error
}

// Batcher is implemented by stores that support batch operations.
type Batcher[T any] interface {
	Batch(ctx context.Context) (Batch[T], error)
}

// InMemoryStore is a simple Store implementation backed by a map.
type InMemoryStore[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

// NewInMemoryStore returns a new InMemoryStore.
func NewInMemoryStore[T any]() *InMemoryStore[T] {
	return &InMemoryStore[T]{items: make(map[string]T)}
}

// Get implements Store.Get.
func (s *InMemoryStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	s.mu.RLock()
	v, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		var zero T
		return zero, false, nil
	}
	return v, true, nil
}

// Set implements Store.Set.
func (s *InMemoryStore[T]) Set(ctx context.Context, key string, value T) error {
	s.mu.Lock()
	s.items[key] = value
	s.mu.Unlock()
	return nil
}

// Keys implements Store.Keys.
func (s *InMemoryStore[T]) Keys(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	keys := make([]string, 0, len(s.items))
	for k := range s.items {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	return keys, nil
}

// Batch implements Batcher.Batch.
func (s *InMemoryStore[T]) Batch(ctx context.Context) (Batch[T], error) {
	return &inMemoryBatch[T]{s: s, sets: make(map[string]T)}, nil
}

type inMemoryBatch[T any] struct {
	s       *InMemoryStore[T]
	sets    map[string]T
	deletes []string
}

func (b *inMemoryBatch[T]) Set(ctx context.Context, key string, value T) error {
	b.sets[key] = value
	return nil
}

func (b *inMemoryBatch[T]) Delete(ctx context.Context, key string) error {
	b.deletes = append(b.deletes, key)
	return nil
}

func (b *inMemoryBatch[T]) Commit(ctx context.Context) error {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	for _, k := range b.deletes {
		delete(b.s.items, k)
	}
	for k, v := range b.sets {
		b.s.items[k] = v
	}
	return nil
}
