# Validator

The `validator` package runs background scans to compare cache entries with the primary storage.

## [Modes](glossary.md#validator-modes)

- `ModeNoop` – only records mismatches.
- `ModeAlert` – suitable for logging or external alerting.
- `ModeAutoHeal` – automatically refreshes the cache from the storage when a mismatch is detected.

## API Reference

### `New`

```go
func New[T any](c cache.Cache[T], s adapter.Store[T], mode Mode, interval time.Duration) *Validator[T]
```
Creates a new Validator.

### `Run`

```go
func (v *Validator[T]) Run(ctx context.Context)
```
Starts the validation loop.

### `Metrics`

```go
func (v *Validator[T]) Metrics() uint64
```
Returns number of mismatches detected.

### `SetDigester`

```go
func (v *Validator[T]) SetDigester(d Digester[T])
```
Sets the digester used for value comparison.

### `Digester` Interface

```go
type Digester[T any] interface {
    Digest(v T) (string, error)
}
```

### `JSONDigester`

Default digester that serializes values using JSON and hashes them with SHA256.

## Usage

```go
cacheLayer := cache.NewInMemory[string]()
v := validator.New[string](cacheLayer, store, validator.ModeAutoHeal, time.Minute)
go v.Run(ctx)
```

`Metrics` reports the number of mismatches found during scans.
