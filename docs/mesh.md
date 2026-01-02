# Warp Mesh (Zero-Infrastructure P2P)

Warp Mesh is a **zero-infrastructure**, peer-to-peer synchronization protocol designed for Go applications running in containerized environments (Kubernetes, Docker Swarm) or simple cluster deployments.

By using UDP Multicast and P2P Gossip, Warp Mesh enables nodes to discover each other and propagate cache invalidations **without requiring an external broker** like Redis, NATS, or Kafka.

## Key Features

- **Zero-Dependency**: No external servers to configure or manage. Just your Go binary.
- **Auto-Discovery**: Nodes automatically find each other using UDP Multicast (where supported) or Static Peer Lists.
- **Efficient Gossip**: Invalidation events are propagated using an optimized gossip protocol.
- **Failure Resilient**: The mesh heals automatically; nodes can join and leave dynamically.

## When to use it?

- **Microservices**: Perfect for sidecar-less architectures or when you want to reduce infrastructure costs.
- **Edge Deployment**: Where deploying a central Redis is impractical or impossible.
- **Simplification**: When you need eventual consistency but want to avoid the operational overhead of a message bus.

## Comparisons

| Feature | Warp Mesh | Redis Pub/Sub | NATS |
| :--- | :--- | :--- | :--- |
| **Infrastructure** | None (P2P) | Redis Server | NATS Cluster |
| **Performance** | High (~1.3M ops/sec) | High | Very High |
| **Complexity** | Low (Lib only) | Medium | Medium |
| **Consistency** | Eventual | Eventual | Eventual |
| **Ordering** | No guarantee | FIFO | FIFO |

## Configuration

Warp Mesh is typically initialized via `presets`.

```go
import "github.com/mirkobrombin/go-warp/v1/syncbus/mesh"

w := presets.NewMeshEventual[MyData](mesh.MeshOptions{
    Port:      7946,               // UDP port for gossip
    Group:     "239.0.0.1",        // Multicast group (optional)
    Interface: "eth0",             // Interface to bind (optional)
    Peers:     []string{"..."},    // Static peers if multicast is disabled
})
```

### Options

| Option | Description | Default |
| :--- | :--- | :--- |
| `Port` | UDP port to bind for listening specific gossip messages. | `7946` |
| `Group` | UDP Multicast group address. | `239.0.0.1` |
| `Interface`| Network interface name to bind multicast to (e.g., "eth0"). | Auto-detect |
| `Peers` | List of known peer addresses (`ip:port`) for unicast/bootstrap in environments without multicast (e.g., cloud VPCs). | `[]` |
| `AdvertiseAddr`| The address this node advertises to others. | Local IP |
| `Heartbeat` | Interval for membership keep-alive messages. | `5s` |

## Limitations

- **No Persistence**: Mesh messages are ephemeral. If a node is down, it misses updates (Warp handles this via TTL/Expiration).
- **Network Policy**: Requires UDP traffic allowed between pods/nodes.
- **Eventual Consistency**: There is no global ordering or strict delivery guarantee (fire-and-forget).
