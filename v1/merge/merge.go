package merge

import (
	"sync"
	"time"
)

// VectorClock represents the logical time of a value across different regions.
// Key: RegionID, Value: Logical Clock Counter.
type VectorClock map[string]uint64

// Copy returns a deep copy of the vector clock.
func (vc VectorClock) Copy() VectorClock {
	n := make(VectorClock, len(vc))
	for k, v := range vc {
		n[k] = v
	}
	return n
}

// Increment increments the clock for the given region.
func (vc VectorClock) Increment(region string) {
	vc[region]++
}

// Merge combines two vector clocks, taking the maximum value for each region.
func (vc VectorClock) Merge(other VectorClock) {
	for k, v := range other {
		if v > vc[k] {
			vc[k] = v
		}
	}
}

// Compare returns:
//
//	1 if vc > other (vc dominates)
//
// -1 if other > vc (other dominates)
//
//	0 if equal
//
// -2 if concurrent (conflict)
func (vc VectorClock) Compare(other VectorClock) int {
	var greater, smaller bool
	// Check all keys in vc
	for k, v := range vc {
		ov := other[k]
		if v > ov {
			greater = true
		} else if v < ov {
			smaller = true
		}
	}
	// Check keys in other that are not in vc
	for k, v := range other {
		if _, ok := vc[k]; !ok && v > 0 {
			smaller = true
		}
	}

	if greater && smaller {
		return -2 // Concurrent
	}
	if greater {
		return 1
	}
	if smaller {
		return -1
	}
	return 0
}

// Value wraps a value with a timestamp and vector clock used by merge strategies.
type Value[T any] struct {
	Data      T
	Timestamp time.Time
	Vector    VectorClock
	Region    string
}

// Strategy defines how two values should be merged.
type Strategy[T any] interface {
	Merge(old, new Value[T]) Value[T]
}

// MergeFn is a custom merge function provided by the developer.
type MergeFn[T any] func(old, new T) (T, error)

// LastWriteWins is the default merge strategy which keeps the most recent value based on physical timestamp.
// It falls back to Vector Clock comparison if available and timestamps are equal.
type LastWriteWins[T any] struct{}

// Merge implements Strategy.Merge.
func (LastWriteWins[T]) Merge(old, new Value[T]) Value[T] {
	// 1. Try Vector Clock dominance
	if old.Vector != nil && new.Vector != nil {
		cmp := new.Vector.Compare(old.Vector)
		if cmp == 1 { // New dominates
			return new
		}
		if cmp == -1 { // Old dominates
			return old
		}
		// If concurrent (-2) or equal (0), fall back to Timestamp
	}

	// 2. Physical Timestamp
	if new.Timestamp.After(old.Timestamp) {
		return new
	}
	return old
}

// Engine manages merge functions for specific keys.
type Engine[T any] struct {
	mu              sync.RWMutex
	custom          map[string]MergeFn[T]
	defaultStrategy Strategy[T]
}

// NewEngine creates a new merge engine with LastWriteWins as default strategy.
func NewEngine[T any]() *Engine[T] {
	return &Engine[T]{custom: make(map[string]MergeFn[T]), defaultStrategy: LastWriteWins[T]{}}
}

// Register associates a custom merge function with a key.
func (e *Engine[T]) Register(key string, fn MergeFn[T]) {
	e.mu.Lock()
	e.custom[key] = fn
	e.mu.Unlock()
}

// Merge merges two values based on the registered function or the default strategy.
func (e *Engine[T]) Merge(key string, old, new Value[T]) (Value[T], error) {
	e.mu.RLock()
	fn, ok := e.custom[key]
	e.mu.RUnlock()

	if ok {
		v, err := fn(old.Data, new.Data)
		if err != nil {
			return Value[T]{}, err
		}
		// When merging data logically, we merge the vector clocks
		mergedVC := old.Vector.Copy()
		if mergedVC == nil {
			mergedVC = make(VectorClock)
		}
		mergedVC.Merge(new.Vector)

		return Value[T]{
			Data:      v,
			Timestamp: new.Timestamp, // Use new timestamp? Or max?
			Vector:    mergedVC,
			Region:    new.Region, // Attribute to new writer
		}, nil
	}
	return e.defaultStrategy.Merge(old, new), nil
}
