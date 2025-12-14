# Federated Caching

Warp supports **Federated Caching** to enable multi-region synchronization with strong consistency guarantees using Vector Clocks. This feature allows geographically distributed clusters to share state and resolve conflicts deterministically.

## Architecture

Federated Caching works by propagating updates across regions via a "Relay" architecture.

1.  **Region ID**: Each Warp cluster is assigned a unique Region ID.
2.  **Gateway/Relay**: A Warp node can be configured as a **Gateway**, bridging the local Sync Bus (e.g., Redis `warp:updates`) to a Federation Sync Bus (e.g., a cross-region Redis `warp:federation`).
3.  **Scope**: Updates can be Local-only or Global. Global updates are forwarded by the Relay to other regions.
4.  **Vector Clocks**: Every value carries a Vector Clock to track causality across regions.

## Architecture

![Federation Architecture](https://mermaid.ink/img/pako:eNptkMFOwzAMhl_F8gmpTdtD2_SEBEhI3DghuI3LNBtN4kRxF1XUu5NsaR0Q0pP9_8_2b-cMrWYEc_S-O5l9_6Q5w-eH1kG_4W5oTva2N2_Pz_f3t2-3t_8aUEFpL6hC-BcoQ_qXoMKq70GFU78FlbC-gUphvYBK47iCStm4hUpi3IJSk3gHlSp5D2p18g9Q6xI_gDqX-Al06ZI_gdpcfAH1ufQP0KivX0CbG19AW1x8Af259A_Q0G_ADxM11g)

*   **Standard Nodes**: Connect only to the Local Bus.
*   **Gateway Nodes**: Connect to both Local Bus and Federation Bus.
*   **Loop Prevention**: The Relay ensures that events originating from the Federation Bus are not echoed back to it.

## Scope & Propagation

By default, invalidations are `ScopeLocal`. To propagate an invalidation globally:

```go
w.Invalidate(ctx, "key", syncbus.WithScope(syncbus.ScopeGlobal))
```

This ensures that:
1.  The event is published to the Local Bus.
2.  The **Gateway** (if present) picks it up, sees `ScopeGlobal`, and relays it to the Federation Bus.
3.  Gateways in other regions receive it from the Federation Bus and publish it to their respective Local Buses.



## Vector Clocks

To handle concurrent updates without a central clock, Warp uses Vector Clocks in the `merge.Value` structure.

```go
type VectorClock map[string]uint64
```

### Conflict Resolution

When a value is updated or merged, the `merge.Engine` compares Vector Clocks:

*   **Domination**: If `V1 > V2`, V1 is kept.
*   **Concurrency**: If neither dominates (concurrent updates), Warp falls back to `Timestamp` (Last-Write-Wins) or custom logic.

## Sync Bus Protocol

The `SyncBus` interface supports publishing with options to propagate consistency metadata.

```go
Publish(ctx, key, syncbus.WithRegion("us-east-1"), syncbus.WithVectorClock(vc))
```

This ensures that when a remote region receives an invalidation, it can decide whether to apply it based on causality.
