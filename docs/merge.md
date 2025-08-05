# Merge Engine

Concurrent writes or cache inconsistencies can be resolved through the `merge` package.

## Strategies

The default strategy is **last-write-wins**. Custom merge functions can be registered for specific keys:

```go
engine := merge.NewEngine[int]()
engine.Register("counter", func(old, new int) (int, error) {
    return old + new, nil
})
```

When `Set` is called, the merge engine compares the existing value with the new one and stores the merged result. Custom functions receive the old and new values and may return an error to abort the merge.
