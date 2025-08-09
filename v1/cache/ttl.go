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
}

// TTLOption mutates TTLOptions.
type TTLOption func(*TTLOptions)

// WithSliding enables sliding expiration for a key.
func WithSliding() TTLOption { return func(o *TTLOptions) { o.Sliding = true } }

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
