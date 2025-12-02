# Leases

Leases provide a mechanism to group multiple keys under a single revocable token.

## API Reference

### `GrantLease`

```go
func (w *Warp[T]) GrantLease(ctx context.Context, ttl time.Duration) (string, error)
```
Creates a new lease and returns its unique ID.
- **ttl**: Duration after which the lease expires automatically (if not revoked).

### `AttachKey`

```go
func (w *Warp[T]) AttachKey(leaseID, key string)
```
Associates a cache key with a lease.
- **Note**: A key can be attached to multiple leases. If *any* of them is revoked, the key is invalidated.

### `RevokeLease`

```go
func (w *Warp[T]) RevokeLease(ctx context.Context, leaseID string)
```
Invalidates the lease. This triggers an invalidation for **all** keys currently attached to this lease, across the entire cluster.

## Use Cases

### 1. User Session Management
Invalidate all user data (profile, settings, session) on logout.

### 2. Feature Flags
Invalidate all configuration keys when a feature flag changes.

### 3. Multi-Tenancy
Clear all data for a specific tenant.
