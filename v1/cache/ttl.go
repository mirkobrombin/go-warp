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
