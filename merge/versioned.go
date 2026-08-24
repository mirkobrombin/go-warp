package merge

import "time"

// VersionedValue keeps a history of values with timestamps.
// It maintains only the most recent `limit` entries.
type VersionedValue[T any] struct {
	versions []Value[T]
	limit    int
}

// NewVersionedValue creates a VersionedValue with the provided limit
// and initializes it with the given value.
func NewVersionedValue[T any](v Value[T], limit int) VersionedValue[T] {
	vv := VersionedValue[T]{limit: limit}
	vv.versions = append(vv.versions, v)
	return vv
}

// Add appends a new value to the history, trimming old entries
// to respect the limit.
func (v *VersionedValue[T]) Add(val Value[T]) {
	v.versions = append(v.versions, val)
	if v.limit > 0 && len(v.versions) > v.limit {
		v.versions = v.versions[len(v.versions)-v.limit:]
	}
}

// Latest returns the most recent value.
func (v VersionedValue[T]) Latest() (Value[T], bool) {
	if len(v.versions) == 0 {
		var zero Value[T]
		return zero, false
	}
	return v.versions[len(v.versions)-1], true
}

// At returns the newest value whose timestamp is not after t.
func (v VersionedValue[T]) At(t time.Time) (Value[T], bool) {
	for i := len(v.versions) - 1; i >= 0; i-- {
		if !v.versions[i].Timestamp.After(t) {
			return v.versions[i], true
		}
	}
	var zero Value[T]
	return zero, false
}
