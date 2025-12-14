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

## Warp Bench Tool

We provide a dedicated benchmarking tool `warp-bench` to verify performance independently.

### Build and Run

```bash
go build -o warp-bench ./cmd/warp-bench/main.go
./warp-bench -c 50 -n 500000 -d 256
```

### Results (Core / In-Memory Standalone)

*Environment: Local Dev Machine*

| Concurrency | Requests | Payload | Throughput | Avg Latency |
|:-----------:|:--------:|:-------:|:----------:|:-----------:|
| 10          | 100k     | 256B    | ~1.6M req/s| 611 ns      |
| 50          | 500k     | 256B    | ~860k req/s| 1157 ns     |
| 100         | 1M       | 256B    | ~1.5M req/s| 630 ns      |

> **Note**: These numbers measure the raw overhead of `Warp Core` (Cache + Store + Bus + Engine integration) in "Standalone" mode (no network IO). They confirm the efficiency of the architecture.
