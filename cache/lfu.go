package cache

// LFUCache provides a cache with a least-frequently-used eviction policy.
//
// It is backed by Ristretto which implements a TinyLFU algorithm.
type LFUCache[T any] struct {
	*RistrettoCache[T]
}

// NewLFU returns a new LFUCache instance.
//
// It reuses the Ristretto implementation under the hood.
func NewLFU[T any](opts ...RistrettoOption) *LFUCache[T] {
	return &LFUCache[T]{NewRistretto[T](opts...)}
}
