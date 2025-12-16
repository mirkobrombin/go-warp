package cache

import "time"

// TTLStrategy provides dynamic TTL values based on access patterns.
// Implementations may adjust and observe key usage to decide the
// appropriate expiration for a cache entry.
type TTLStrategy interface {
	// Record notifies the strategy of an access to the key.
	Record(key string)
	// TTL returns the TTL to apply for the key.
	TTL(key string) time.Duration
}

// TTLOptions configures TTL behavior for cache entries.
//
// When Sliding is true, accessing a key resets its expiration using the
// current TTL. The other fields enable simple dynamic adjustments of the TTL
// based on the time elapsed between consecutive accesses.
//
// If the elapsed time between two accesses is less than FreqThreshold the TTL
// is increased by Increment, up to MaxTTL if set. Otherwise the TTL is
// decreased by Decrement but not below MinTTL.
type TTLOptions struct {
	Sliding       bool
	FreqThreshold time.Duration
	Increment     time.Duration
	Decrement     time.Duration
	MinTTL        time.Duration
	MaxTTL        time.Duration
	FailSafeGracePeriod time.Duration
	SoftTimeout         time.Duration
	EagerRefreshThreshold float64
}

// TTLOption mutates TTLOptions.
type TTLOption func(*TTLOptions)

// WithSliding enables sliding expiration for a key.
func WithSliding() TTLOption { return func(o *TTLOptions) { o.Sliding = true } }

// WithFailSafe enables the stale-if-error pattern.
// If the backend fails, the cache will return the expired value if it is within the grace period.
func WithFailSafe(grace time.Duration) TTLOption {
	return func(o *TTLOptions) {
		o.FailSafeGracePeriod = grace
	}
}

// WithSoftTimeout sets a timeout for backend fetch operations.
// If the backend takes longer than the duration, the cache returns the stale value (if available)
// instead of waiting or failing.
func WithSoftTimeout(d time.Duration) TTLOption {
	return func(o *TTLOptions) {
		o.SoftTimeout = d
	}
}

// WithEagerRefresh enables proactive background refreshing of cache entries.
// If an item's remaining TTL falls below the specified threshold (e.g., 0.1 for 10%),
// a refresh is triggered in the background, serving the current item immediately.
// The threshold must be between 0.0 and 1.0.
func WithEagerRefresh(threshold float64) TTLOption {
	return func(o *TTLOptions) {
		if threshold >= 0.0 && threshold <= 1.0 {
			o.EagerRefreshThreshold = threshold
		}
	}
}

// WithDynamicTTL configures simple frequency based TTL adjustments.
//
// freq defines the maximum duration between two accesses for the key to be
// considered "hot". When a hot access is detected the TTL is increased by inc
// up to max. Cold accesses reduce the TTL by dec but not below min.
func WithDynamicTTL(freq, inc, dec, min, max time.Duration) TTLOption {
	return func(o *TTLOptions) {
		o.FreqThreshold = freq
		o.Increment = inc
		o.Decrement = dec
		o.MinTTL = min
		o.MaxTTL = max
	}
}

// Adjust updates the TTL based on the configured options and returns the new
// TTL. last is the time of the previous access; now is the current time.
func (o TTLOptions) Adjust(cur time.Duration, last, now time.Time) time.Duration {
	if o.FreqThreshold <= 0 {
		return cur
	}
	if now.Sub(last) <= o.FreqThreshold {
		cur += o.Increment
		if o.MaxTTL > 0 && cur > o.MaxTTL {
			cur = o.MaxTTL
		}
	} else {
		cur -= o.Decrement
		if cur < o.MinTTL {
			cur = o.MinTTL
		}
	}
	return cur
}
