package merge

import "time"

// Value wraps a value with a timestamp used by merge strategies.
type Value struct {
	Data      any
	Timestamp time.Time
}

// Strategy defines how two values should be merged.
type Strategy interface {
	Merge(old, new Value) Value
}

// MergeFn is a custom merge function provided by the developer.
type MergeFn func(old, new any) (any, error)

// LastWriteWins is the default merge strategy which keeps the most recent value.
type LastWriteWins struct{}

// Merge implements Strategy.Merge.
func (LastWriteWins) Merge(old, new Value) Value {
	if new.Timestamp.After(old.Timestamp) {
		return new
	}
	return old
}

// Engine manages merge functions for specific keys.
type Engine struct {
	custom          map[string]MergeFn
	defaultStrategy Strategy
}

// NewEngine creates a new merge engine with LastWriteWins as default strategy.
func NewEngine() *Engine {
	return &Engine{custom: make(map[string]MergeFn), defaultStrategy: LastWriteWins{}}
}

// Register associates a custom merge function with a key.
func (e *Engine) Register(key string, fn MergeFn) {
	e.custom[key] = fn
}

// Merge merges two values based on the registered function or the default strategy.
func (e *Engine) Merge(key string, old, new Value) (Value, error) {
	if fn, ok := e.custom[key]; ok {
		v, err := fn(old.Data, new.Data)
		if err != nil {
			return Value{}, err
		}
		return Value{Data: v, Timestamp: new.Timestamp}, nil
	}
	return e.defaultStrategy.Merge(old, new), nil
}
