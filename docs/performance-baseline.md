# Performance Baseline (Section 10)

This document tracks the baseline benchmark and profiling workflow for Release 1.

## Benchmark Suite

- Location: `internal/blob/bench_test.go`
- Benchmarks:
  - `BenchmarkObjectIO/1KB`
  - `BenchmarkObjectIO/1MB`
  - `BenchmarkObjectIO/100MB`

Run:

```bash
make bench
```

## Profiling Harness

Generate CPU and memory profiles for object IO workload:

```bash
make profile
```

Outputs:

- `profiles/cpu.out`
- `profiles/mem.out`

Inspect profiles:

```bash
go tool pprof profiles/cpu.out
go tool pprof profiles/mem.out
```

## Initial Baseline Record

Measured on Linux amd64 (`AMD Ryzen 9 7900X`) using:

```bash
make profile
```

- `1KB`: `27,456 ns/op`, `37.30 MB/s`, `7,993 B/op`, `39 allocs/op`
- `1MB`: `2,057,941 ns/op`, `509.53 MB/s`, `4,290,748 B/op`, `50 allocs/op`
- `100MB`: `178,270,395 ns/op`, `588.19 MB/s`, `268,618,077 B/op`, `60 allocs/op`
- CPU profile artifact: `profiles/cpu.out`
- Memory profile artifact: `profiles/mem.out`
