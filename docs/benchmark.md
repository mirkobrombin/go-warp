# Cache Benchmarks

Benchmark results generated with `go test -bench . -benchmem`.

```
$ go test -bench . -benchmem ./v1/cache
BenchmarkInMemoryCacheSet-5      1000000              1750 ns/op             372 B/op          3 allocs/op
BenchmarkInMemoryCacheGet-5      9723705               122.0 ns/op             0 B/op          0 allocs/op
BenchmarkRistrettoCacheSet-5      281559              3730 ns/op             426 B/op         12 allocs/op
BenchmarkRistrettoCacheGet-5     7991107               150.2 ns/op            19 B/op          1 allocs/op
BenchmarkRedisCacheSet-5           38400             34865 ns/op            1404 B/op         37 allocs/op
BenchmarkRedisCacheGet-5           39525             29205 ns/op             536 B/op         23 allocs/op
```

## Comparison

* **InMemoryCache** is the fastest: ~122 ns for `Get` with no allocations and ~1.75 µs for `Set` with few allocations.
* **Ristretto** offers comparable read performance (~150 ns) but uses more memory and allocations, while writes are about 2× slower.
* **Redis** (through an in-memory server) is significantly slower (~29–35 µs) and uses much more memory; network and serialization overhead impact performance.

