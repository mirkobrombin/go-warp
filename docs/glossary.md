# Glossary

Definitions of common Warp terms with links to the relevant module documentation.

## LRU
Least Recently Used caching policy where the oldest accessed item is evicted first. See [Cache](cache.md#lru-cache).

## LFU/TinyLFU
Least Frequently Used eviction strategy backed by the [Ristretto](https://github.com/dgraph-io/ristretto) library. See [Cache](cache.md#lfu-cache).

## Sliding TTL
A Time To Live that resets on each access. Warp also supports dynamic TTLs that grow or shrink based on access frequency. See [Cache](cache.md#sliding-and-dynamic-ttl).

## NATS
A lightweight messaging system used for distributed invalidations. See [Sync Bus](syncbus.md#nats-bus).

## Kafka
A distributed log used as a bus for cache invalidations. See [Sync Bus](syncbus.md#kafka-bus).

## Ristretto
High performance caching library providing TinyLFU functionality. Used by Warp's LFU cache. See [Cache](cache.md#lfu-cache).

## Leases
Revocable tokens that group keys so they can be invalidated together. See [Leases](leases.md).

## Watch Bus
Lightweight message bus for streaming payloads. See [Watch Bus](watchbus.md).

## Sync Bus
Pluggable interface for propagating invalidations across nodes. See [Sync Bus](syncbus.md).

## Validator modes
Background scan behaviours that detect cache and storage mismatches. Modes include `ModeNoop`, `ModeAlert`, and `ModeAutoHeal`. See [Validator](validator.md#modes).

