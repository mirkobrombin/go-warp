// Package lock provides distributed locking utilities with in-memory and Redis
// implementations. Locks propagate across nodes via syncbus, enabling
// coordination patterns such as leader election. Locks can have an optional TTL
// to avoid deadlocks.
package lock
