# Best Practices

This guide provides recommendations for common scenarios when using Warp.

## Scenarios

### 1. High Read, Low Write (e.g., Product Catalog)
**Goal**: Maximize read performance, tolerate slight staleness.

- **Mode**: `ModeEventualDistributed`
- **Cache**: `InMemory` with `LRU`
- **TTL**: Long (e.g., 1 hour) with `SlidingTTL` enabled.
- **Strategy**: Use `Warmup` on startup to load popular products.

### 2. User Sessions (Critical Security)
**Goal**: Immediate invalidation on logout.

- **Mode**: `ModeEventualDistributed` (or `StrongDistributed` if you need absolute guarantees).
- **Leases**: Use Leases to group all session keys for a user. Revoke the lease on logout.
- **TTL**: Short (e.g., 15 mins) with `SlidingTTL`.

### 3. Distributed Counters / Rate Limiting
**Goal**: Atomic updates across nodes.

- **Mode**: `ModeStrongDistributed`
- **Merge Engine**: Register a custom merge function (e.g., `func(old, new int) int { return old + new }`).
- **Quorum**: Set quorum to `N/2 + 1` for fault tolerance.

### 4. Ephemeral Data (e.g., Job Status)
**Goal**: Fast local access, no persistence needed.

- **Mode**: `ModeStrongLocal`
- **Store**: `nil` (or `InMemoryStore` if you need local persistence).
- **TTL**: Short.

## General Advice

### Don't
- **Don't use `ModeStrongDistributed` for everything.** It adds latency (network round-trips). Use it only when strictly necessary.
- **Don't ignore errors.** Especially from `Set` or `Txn.Commit`. They might indicate a bus failure or quorum issue.
- **Don't cache everything.** Caching has overhead. Cache only what is read frequently or expensive to compute.

### Do
- **Do use `Register` explicitly.** It documents your data model and consistency requirements.
- **Do monitor Metrics.** Set up Prometheus alerts for high `warp_core_misses_total` or `warp_bus_errors`.
- **Do use `Validator`.** It's cheap insurance against data drift.
- **Do use `Batcher`.** If your DB supports it, implement the `Batcher` interface in your Adapter for massive write performance gains.
