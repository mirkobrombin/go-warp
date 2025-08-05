package merge

import "time"

// Value wraps a value with a timestamp used by merge strategies.
type Value[T any] struct {
	Data      T
	Timestamp time.Time
}

// Strategy defines how two values should be merged.
type Strategy[T any] interface {
	Merge(old, new Value[T]) Value[T]
}

// MergeFn is a custom merge function provided by the developer.
type MergeFn[T any] func(old, new T) (T, error)

// LastWriteWins is the default merge strategy which keeps the most recent value.
type LastWriteWins[T any] struct{}

// Merge implements Strategy.Merge.
func (LastWriteWins[T]) Merge(old, new Value[T]) Value[T] {
	if new.Timestamp.After(old.Timestamp) {
		return new
	}
	return old
}

// Engine manages merge functions for specific keys.
type Engine[T any] struct {
	custom          map[string]MergeFn[T]
	defaultStrategy Strategy[T]
}

// NewEngine creates a new merge engine with LastWriteWins as default strategy.
func NewEngine[T any]() *Engine[T] {
	return &Engine[T]{custom: make(map[string]MergeFn[T]), defaultStrategy: LastWriteWins[T]{}}
}

// Register associates a custom merge function with a key.
func (e *Engine[T]) Register(key string, fn MergeFn[T]) {
	e.custom[key] = fn
}

// Merge merges two values based on the registered function or the default strategy.
func (e *Engine[T]) Merge(key string, old, new Value[T]) (Value[T], error) {
	if fn, ok := e.custom[key]; ok {
		v, err := fn(old.Data, new.Data)
		if err != nil {
			return Value[T]{}, err
		}
		return Value[T]{Data: v, Timestamp: new.Timestamp}, nil
	}
	return e.defaultStrategy.Merge(old, new), nil
}
