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

## Confronto

* **InMemoryCache** è il più veloce: ~122 ns per `Get` senza allocazioni e ~1.75 µs per `Set` con poche allocazioni.
* **Ristretto** offre prestazioni comparabili in lettura (~150 ns) ma richiede più memoria e allocazioni, mentre le scritture sono circa 2× più lente.
* **Redis** (tramite server in-memory) è significativamente più lento (∼29–35 µs) e utilizza molta più memoria; il costo di rete e serializzazione incide sulle prestazioni.

## Conclusioni

Per carichi in-process, `InMemoryCache` è la scelta più performante sia in termini di latenza che di utilizzo memoria.
`Ristretto` rappresenta un'alternativa valida quando servono politiche di eviction più avanzate ma introduce overhead.
Soluzioni basate su **Redis** sono ordini di grandezza più lente e allocano molta memoria, rendendole adatte quando la persistenza o la condivisione di rete sono requisiti fondamentali anziché la pura velocità.

BigCache non è presente nel repository e quindi non è stato benchmarkato.
