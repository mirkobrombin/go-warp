# StreamBus

`streambus` is Warp's transport-independent path for sustained or bursty data
flows. It complements `watchbus`: WatchBus is intentionally small and lossy,
while StreamBus makes buffering, overload, replay, reliability, and priority
part of the public contract.

## In-memory bus

```go
bus := streambus.NewInMemory(streambus.Config{
    DefaultBuffer:  256,
    MaxBuffer:      4096,
    ReplayCapacity: 512,
    MaxPayloadBytes: 16 << 20,
})
defer bus.Close()

updates, err := bus.Subscribe(ctx, streambus.SubscribeOptions{
    Topic:    "component:metrics",
    Buffer:   256,
    Overflow: streambus.LatestOnly,
    Replay:   1,
})
if err != nil {
    return err
}
defer updates.Close()

_, err = bus.Publish(ctx, streambus.Frame{
    Topic:       "component:metrics",
    Payload:     encodedPatch,
    Reliability: streambus.Unreliable,
    Priority:    streambus.PriorityInteractive,
})
```

Exact-topic subscriptions use a map lookup. Prefix subscriptions use a byte
trie and do not require scanning every subscription.

## Overload policies

| Policy | Behavior | Typical data |
| --- | --- | --- |
| `Block` | Wait for subscriber capacity | commands, audit events |
| `DropNewest` | Preserve queued history | bounded event feeds |
| `DropOldest` | Admit new frames, discard oldest | rolling telemetry |
| `LatestOnly` | Collapse queued frames to current state | cursor, UI state, gauges |

Every subscription exposes `Stats()`. Dropped frames are observable rather than
silently discarded.

`Block` propagates pressure back through `Publish`. Give blocking subscribers a
deadline or cancellable context so a stalled client cannot stop a producer
forever.

## Replay and resumption

Set `Config.ReplayCapacity` to retain a per-topic ring of recent frames. An
exact-topic subscriber can request the last `N` frames with `Replay`. Each
published frame receives a monotonic sequence number. A reconnecting client can
set `SubscribeOptions.Since` to its last acknowledged sequence. StreamBus then
replays every retained newer frame or returns `ErrReplayUnavailable` when a
known gap would otherwise be hidden.

Replay is process-local in `InMemory`. Durable or cross-node replay should use a
future persistent StreamBus backend or an application journal.

## WebTransport

`streambus/webtransport.Handler` exposes a bus through WebTransport over
HTTP/3. It uses:

- one reliable bidirectional control stream for subscribe, unsubscribe,
  publish, acknowledgement, ping, and errors;
- one independent reliable unidirectional stream per subscription;
- congestion-controlled datagrams for `Unreliable` frames.

Independent streams prevent a large snapshot or a lost packet in one topic from
blocking unrelated interactive updates. Datagrams suit replaceable state where
freshness matters more than delivery of every intermediate value.

```go
mux := http.NewServeMux()
h3 := &http3.Server{
    Addr:      ":443",
    TLSConfig: tlsConfig,
    Handler:   mux,
    EnableDatagrams: true,
    QUICConfig: &quic.Config{
        EnableDatagrams:                  true,
        EnableStreamResetPartialDelivery: true,
    },
}
wtServer := &webtransport.Server{H3: h3}
webtransport.ConfigureHTTP3Server(h3)

handler := &warpwebtransport.Handler{
    Server: wtServer,
    Bus:    bus,
    Authenticate: func(r *http.Request) error {
        return authenticate(r)
    },
}

mux.Handle("/stream", handler)
log.Fatal(wtServer.ListenAndServe())
```

The handler must be mounted on the same HTTP/3 server referenced by
`webtransport.Server`. WebTransport requires TLS. Keep WebSocket or HTTP
streaming as a fallback for clients and network paths that cannot establish
HTTP/3. Unreliable messages larger than `MaxDatagramBytes` automatically fall
back to the subscription's reliable stream rather than disappearing.

## Binary protocol

`EncodeMessage`, `DecodeMessage`, `WriteMessage`, and `NewReader` implement the
versioned StreamBus wire format. Reliable streams use unsigned-varint length
framing. Datagrams contain one unframed message. Payloads remain opaque binary
data so callers can use CBOR, MessagePack, Protocol Buffers, or a custom patch
codec without JSON or base64 expansion.

The protocol separates transport delivery from application encoding. Warp does
not compress payloads automatically because already-compressed media and tiny
patches often become larger. Compress large application payloads before
publishing when measurements justify it.

## Choosing between WatchBus and StreamBus

Use WatchBus for simple notifications and existing Redis or NATS watch paths.
Use StreamBus when any of these are required:

- explicit backpressure or drop behavior;
- sustained high fan-out;
- replay or sequence tracking;
- independent reliable streams;
- unreliable low-latency updates;
- binary payloads over WebTransport.
