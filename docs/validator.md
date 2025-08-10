# Validator

The `validator` package runs background scans to compare cache entries with the primary storage.

## [Modes](glossary.md#validator-modes)

- `ModeNoop` – only records mismatches.
- `ModeAlert` – suitable for logging or external alerting.
- `ModeAutoHeal` – automatically refreshes the cache from the storage when a mismatch is detected.

## Usage

```go
cacheLayer := cache.NewInMemory[string]()
v := validator.New[string](cacheLayer, store, validator.ModeAutoHeal, time.Minute)
go v.Run(ctx)
```

`Metrics` reports the number of mismatches found during scans.
