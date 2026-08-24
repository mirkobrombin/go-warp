# Migrating to Warp v2

Warp v2 changes the module identity and public import paths. It also moves to
Foundation v2 and raises the minimum Go version to 1.25.

## Module path

Update the requirement:

```sh
go get github.com/mirkobrombin/go-warp/v2@latest
```

Replace imports such as:

```go
import "github.com/mirkobrombin/go-warp/v1/core"
```

with:

```go
import "github.com/mirkobrombin/go-warp/v2/core"
```

The package names remain the same. Source packages now live at the module root,
so the repository layout matches the public `/v2/<package>` path.

## Foundation

Warp now imports:

```text
github.com/mirkobrombin/go-foundation/v2
```

The synchronization bus uses `core/safemap` and `core/resiliency` from
Foundation v2. Applications that also import Foundation should remove any
remaining `github.com/mirkobrombin/go-foundation` v1 requirement.

## Behavior changes

- `NATSBus.Subscribe` confirms the server subscription before returning.
- NATS reconnection restores every active subscription before swapping the
  connection.
- `GetOrSet` stores a loaded value before releasing its singleflight entry and
  checks the cache again inside the flight.
- In-memory, Redis, Kafka, and NATS subscriptions serialize delivery with
  channel closure, so unsubscribe cannot race with a publish.
- A custom merge can load its previous value from the store when L1 has been
  invalidated. Merge results no longer depend on that value still being cached.

These changes preserve existing public method signatures under the new major
module path.

## Verification

After changing imports:

```sh
go mod tidy
go build ./...
go test -race ./...
go vet ./...
```

The repository CI also runs Foundation checks, `govulncheck`, and a blocking
EUProvGuard scan.
