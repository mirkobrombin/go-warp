# Leases

Warp includes a simple lease manager that allows grouping keys under a
revocable lease. Leases are renewed periodically until explicitly
revoked or the process stops.

## Granting a Lease

```go
id, err := w.GrantLease(ctx, time.Minute)
if err != nil {
    // handle error
}
```

## Attaching Keys

Keys can be associated with a lease. When the lease is revoked the keys
are invalidated locally and the revocation is propagated through the
`syncbus`.

```go
w.AttachKey(id, "example")
```

## Revoking

```go
w.RevokeLease(ctx, id)
```

All nodes subscribed to the same lease will receive the revocation event
and invalidate the attached keys.
