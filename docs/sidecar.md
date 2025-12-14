# Warp Sidecar (Proxy Mode)

Warp provides a `warp-proxy` binary that acts as a sidecar, exposing the core Warp logic via the standard **Redis Serialization Protocol (RESP)**. This allows applications written in any language (Python, Node.js, Ruby, etc.) to use Warp as if it were a local Redis instance, gaining the benefits of intelligent caching and distributed consistency without embedding the Go library.

## Architecture

```mermaid
graph LR
    App[Legacy App (Node/Python)] -- RESP (localhost:6380) --> Sidecar[Warp Sidecar]
    Sidecar -- SyncBus --> Cluster[Warp Cluster / Redis]
```

The sidecar runs alongside your application container/process. It maintains an in-memory L1 cache and handles distributed coordination (SyncBus) transparently.

## Installation

```bash
go install github.com/mirkobrombin/go-warp/cmd/warp-proxy@latest
```

## Usage

Start the proxy server:

```bash
warp-proxy -port 6380 -addr 0.0.0.0
```

### Supported Commands

The proxy supports a subset of Redis commands required for basic Caching:

| Command | Description | Warp Equivalent |
| :--- | :--- | :--- |
| `GET <key>` | Get value. | `core.Get(ctx, key)` (Auto-registers if missing) |
| `SET <key> <val>` | Set value. | `core.Set(ctx, key, val)` |
| `PING` | Health check. | - |

> [!IMPORTANT]
> **Auto-Registration**: When using the proxy, keys accessed for the first time are automatically registered with `ModeStrongLocal` and a default TTL (5 minutes). This is a convenience for sidecar usage.

## Polyglot Example (Node.js)

```javascript
/* Using any standard Redis client */
const Redis = require("ioredis");
const warp = new Redis(6380); // Connect to sidecar

async function main() {
    await warp.set("my-key", "Hello from Node");
    const val = await warp.get("my-key");
    console.log(val); // "Hello from Node"
}
main();
```
