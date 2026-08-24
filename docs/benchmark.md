# Cache Benchmarks

Recorded on 2026-08-24 with Go 1.26.0 on linux/amd64 and an Intel
i7-13700H. Results vary by CPU, scheduler, and dependency version.

```
$ go test -run '^$' -bench . -benchmem ./cache
BenchmarkInMemoryCacheSet-20      1826172       621.8 ns/op      352 B/op       4 allocs/op
BenchmarkInMemoryCacheGet-20     11826658        99.89 ns/op       0 B/op       0 allocs/op
BenchmarkRistrettoCacheSet-20      567345      3926 ns/op        409 B/op      13 allocs/op
BenchmarkRistrettoCacheGet-20     9053079       136.0 ns/op       20 B/op       1 allocs/op
BenchmarkRedisCacheSet-20           29900     48861 ns/op       1890 B/op      40 allocs/op
BenchmarkRedisCacheGet-20           28083     36518 ns/op        921 B/op      27 allocs/op
```

## Comparison

* **InMemoryCache** reads in about 100 ns without allocating and writes in about 622 ns.
* **Ristretto** reads in about 136 ns and writes in about 3.9 microseconds.
* **Redis** through miniredis reads in about 36.5 microseconds and writes in about 48.9 microseconds.

## Warp Bench Tool

The `warp-bench` command measures the complete in-memory Warp path.

### Build and Run

```bash
go build -o warp-bench ./cmd/warp-bench/main.go
./warp-bench -c 50 -n 500000 -d 256
```

### Results (Core / In-Memory Standalone)

Environment: the same machine and toolchain recorded above.

| Concurrency | Requests | Payload | Throughput | Avg Latency |
|:-----------:|:--------:|:-------:|:----------:|:-----------:|
| 10          | 100k     | 256B    | 1.88M req/s | 533 ns      |
| 50          | 500k     | 256B    | 1.31M req/s | 762 ns      |
| 100         | 1M       | 256B    | 1.17M req/s | 854 ns      |

These numbers measure the in-memory standalone path without network I/O.
