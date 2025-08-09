package cache

// Strategy defines the cache eviction policy used by cache.New.
type Strategy int

const (
	// LRUStrategy uses a least-recently-used eviction policy.
	LRUStrategy Strategy = iota
	// LFUStrategy uses a least-frequently-used eviction policy.
	LFUStrategy
	// AdaptiveStrategy switches between LRU and LFU based on access patterns.
	AdaptiveStrategy
)

// Option configures cache.New.
type Option[T any] func(*factoryConfig[T])

type factoryConfig[T any] struct {
	strategy Strategy
}

// WithStrategy selects the eviction strategy to use. The default is LRUStrategy.
func WithStrategy[T any](s Strategy) Option[T] {
	return func(cfg *factoryConfig[T]) {
		cfg.strategy = s
	}
}

// New returns a Cache using the selected strategy.
//
// By default an LRU cache is created. LFU and Adaptive strategies can be
// requested via WithStrategy.
func New[T any](opts ...Option[T]) Cache[T] {
	cfg := factoryConfig[T]{strategy: LRUStrategy}
	for _, opt := range opts {
		opt(&cfg)
	}
	switch cfg.strategy {
	case LFUStrategy:
		return NewLFU[T]()
	case AdaptiveStrategy:
		return NewAdaptive[T]()
	default:
		return NewLRU[T]()
	}
}
