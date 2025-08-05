// Package cache provides caching utilities for go-warp. The in-memory cache
// spawns a background goroutine that periodically sweeps expired entries. The
// sweep interval can be customized through options when creating the cache.
package cache
