package cache

// LRUCache is an in-memory cache using a least-recently-used eviction policy.
//
// It is an alias of InMemoryCache for clarity when selecting cache strategies.
type LRUCache[T any] = InMemoryCache[T]

// NewLRU returns a new LRUCache instance.
func NewLRU[T any](opts ...InMemoryOption[T]) *LRUCache[T] {
	return NewInMemory[T](opts...)
}
