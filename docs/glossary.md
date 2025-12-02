# Glossary

Definitions of common Warp terms with links to the relevant module documentation.

## Adapter
An abstraction layer for persistent storage. Warp uses adapters to fetch data on cache misses (`Get`) and persist data on updates (`Set`). See [Adapter](adapter.md).

## Batcher
An interface extension for [Adapters](#adapter) that supports batched operations. If an adapter implements `Batcher`, Warp's [Transactions](#transaction) will use it to optimize writes.

## Bus
See [Sync Bus](#sync-bus).

## Consistency Mode
A configuration per key that determines how Warp handles data consistency. Modes include `StrongLocal`, `EventualDistributed`, and `StrongDistributed`. See [Core](core.md#registration).

## Invalidation
The process of removing a key from the cache. In distributed modes, invalidations are propagated to other nodes via the [Sync Bus](#sync-bus).

## Kafka
A distributed event streaming platform used as a [Sync Bus](#sync-bus) implementation for high-throughput environments.

## Lease
A mechanism to group multiple keys under a single revocable token. Revoking a lease invalidates all associated keys across the cluster. Useful for user sessions or feature flags. See [Leases](leases.md).

## LFU (Least Frequently Used)
A cache eviction policy that discards the least frequently used items first. Warp uses [TinyLFU](#tinylfu) via the Ristretto library.

## Lock
A distributed locking primitive built on top of the [Sync Bus](#sync-bus). Allows nodes to coordinate exclusive access to resources. See [Lock](lock.md).

## LRU (Least Recently Used)
A cache eviction policy that discards the least recently used items first. Simple and effective for many workloads.

## Merge Engine
A component that resolves conflicts when updating data. It allows registering custom merge functions (e.g., incrementing a counter, appending to a list) instead of simple overwrites. See [Merge](merge.md).

## NATS
A high-performance messaging system used as a [Sync Bus](#sync-bus) implementation. Ideal for low-latency invalidation propagation.

## Quorum
The minimum number of nodes that must acknowledge a write operation in `StrongDistributed` mode. Ensures data durability and consistency across the cluster.

## Registration
The act of defining configuration (Consistency Mode, TTL, etc.) for a specific key pattern in Warp. Keys must be registered before use.

## Ristretto
A high-performance Go cache library used by Warp for its LFU implementation.

## Sharding
Warp internally shards key registrations to reduce lock contention and improve concurrency.

## Sliding TTL
A Time-To-Live strategy where the expiration time is extended upon every access. Keeps frequently accessed data hot.

## Strong Distributed
A [Consistency Mode](#consistency-mode) that guarantees strong consistency across the cluster using [Quorum](#quorum) writes.

## Sync Bus
The communication channel used by Warp to propagate invalidations and coordinate distributed operations (locks, strong consistency). Implementations include In-Memory, NATS, Kafka, and Redis.

## TinyLFU
An admission policy that uses a probabilistic data structure to estimate access frequency, preventing one-time wonders from polluting the cache.

## Transaction
A set of operations (`Set`, `Delete`, `CompareAndSwap`) applied atomically. Warp transactions support custom merge logic and batched storage writes.

## TTL (Time To Live)
The duration for which an item remains valid in the cache.

## TTL Strategy
A dynamic policy for calculating TTLs based on access patterns or other logic. See [Adaptive TTL](cache.md#adaptive-ttl).

## Validator
A background process that scans the cache and storage to detect and optionally repair inconsistencies. Modes include `Noop`, `Alert`, and `AutoHeal`.

## Versioned Cache
A cache variant that stores historical versions of data, allowing retrieval of values at a specific point in time (`GetAt`).

## Warp
The core struct and entry point of the library. It orchestrates the Cache, Store, Bus, and Merge Engine.

## Watch Bus
A lightweight, separate bus for streaming arbitrary byte payloads, useful for real-time updates unrelated to cache invalidation.
