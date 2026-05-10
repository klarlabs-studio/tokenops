# Performance

The proxy is on the request hot path. Latency is gated in CI; this
page documents the gates and the levers when something is slow.

## CI gate

`internal/proxy/bench_test.go::TestProxyP99OverheadGate` runs on
every PR. It sends 200 paired requests (direct upstream vs through
proxy with full observer) and asserts the proxy's p99 overhead stays
under 50ms. Override the threshold via `TOKENOPS_BENCH_P99_MS` (CI
uses 200ms to absorb noisy runners).

Local benchmarks:

```bash
make bench       # informational microbenchmarks
make bench-gate  # the same threshold check the CI gate runs
```

Reference numbers on an Apple M-series laptop:

```
BenchmarkProxyForwardBaseline    35µs/op   (direct upstream)
BenchmarkProxyForward            74µs/op   (through proxy, no observer)
BenchmarkProxyForwardObserver    81µs/op   (full observer wired)
BenchmarkProxySSE                92µs/op   (small SSE stream)
```

## Levers when overhead grows

| Symptom                          | Lever                                     |
|----------------------------------|-------------------------------------------|
| Bus `dropped` count > 0          | Bump `events.QueueCapacity`               |
| Tokenizer dominates flame graph  | Disable preflight, rely on response usage |
| Cache hit rate < 5% in workload  | Lower TTL or increase MaxEntries          |
| OTLP exporter slowing the bus    | Move to a local collector with batching   |

## Optimizer latency budget

Each `Pipeline.Run` enforces `LatencyBudget`. Optimizers that would
push past the budget are skipped with an attributed event. Default
budget is unset (no cap); set per-call when enabling interactive mode
on the request hot path.
