# Telemetry benchmarks

GoKG keeps raw Go benchmark output so performance claims can be traced back to
the exact revision and machine that produced them. The local workflow has three
parts:

1. a regression test proves that high-cardinality aggregation state is bounded;
2. repeated Go benchmarks measure latency and allocation traffic;
3. a dependency-free comparator summarizes matching baseline and current runs.

## Run locally

Run the complete telemetry benchmark suite with ten samples per benchmark:

```sh
./scripts/bench-telemetry.sh /tmp/telemetry-current.txt
```

The same command is available through Make:

```sh
make bench-telemetry BENCH_OUTPUT=/tmp/telemetry-current.txt
```

The raw output is written to the requested path. A sibling `.meta` file records
the UTC timestamp, Go version, GOOS/GOARCH, CPU model, Git commit and dirty
state, a conservative hash of the benchmark harness, benchmark settings, and
the exact command. By default, the runner also executes the 10,000-unique-value
cardinality regression test and a short discarded warm-up.

The runner refuses to overwrite an existing result. It writes private temporary
files in the destination directory and publishes both files only after the Go
benchmark exits successfully. A per-result lock prevents concurrent capture or
comparison. When no output path is supplied, it creates a private, unpredictable
directory under `TMPDIR` and prints the resulting path.

Useful controls:

```sh
# Use the same scheduler width in every compared run.
BENCH_CPU=1 BENCH_COUNT=10 BENCH_TIME=1s \
  ./scripts/bench-telemetry.sh /tmp/telemetry-current.txt

# Run only one benchmark.
BENCH_REGEX='^BenchmarkReportBuilderHighCardinality$' \
  ./scripts/bench-telemetry.sh /tmp/high-cardinality.txt

# Run only the bounded-cardinality regression test.
make bench-telemetry-bounds
```

`BENCH_CPU` accepts one positive integer, not a list. The comparator requires
the Go CPU suffix to match so results from different scheduler widths are not
silently combined.

## Capture a fair baseline and comparison

The baseline and current run must use:

- identical benchmark source and input size;
- the same machine, Go toolchain, `BENCH_CPU`, `BENCH_TIME`, and `BENCH_COUNT`;
- the same power mode, with thermal throttling and background work minimized;
- unprofiled runs for the numbers used in the comparison.

Capture the raw result at the baseline revision before changing the code, then
capture the current revision:

```sh
# Run in a checkout/worktree at the baseline revision.
BENCH_CPU=1 ./scripts/bench-telemetry.sh /tmp/telemetry-before.txt

# Run in a checkout/worktree at the current revision.
BENCH_CPU=1 ./scripts/bench-telemetry.sh /tmp/telemetry-after.txt

./scripts/compare-telemetry-benchmarks.sh \
  /tmp/telemetry-before.txt /tmp/telemetry-after.txt
```

Or use Make for the last step:

```sh
make bench-telemetry-compare \
  BEFORE=/tmp/telemetry-before.txt AFTER=/tmp/telemetry-after.txt
```

The comparator requires the final Go CPU suffix to match, groups repeated
samples by benchmark and metric, and reports each median. It rejects incomplete
or locked captures and warns when the `.meta` sidecars show different benchmark
harnesses, Go versions, machines, benchmark settings, or dirty worktrees. Its
`Delta` is:

```text
(current - baseline) / baseline * 100
```

Negative delta is better for `ns/op`, `B/op`, and `allocs/op`. Positive delta
is better for `MB/s`. A median is a robust descriptive summary, not a
statistical significance test; use `benchstat` on the same raw files when a
confidence interval and hypothesis test are required.

Do not reconstruct a historical baseline from a claimed percentage. If the
old revision did not contain the same benchmark and its raw output was not
saved, the old number is not reproducible without first applying the same
benchmark harness to that revision.

## How to read the reported numbers

`BenchmarkReportBuilderHighCardinality` prepares 10,000 events before timing.
One benchmark operation processes the complete 10,000-event batch, so a result
of `9,900,000 ns/op` means `9.9 ms` per batch, not per-event request latency.
Dividing by 10,000 gives an amortized processing cost of `0.99 us/event` for
that batch, including final report construction and sorting.

For an example baseline of `31.2 ms/op` and a current result of `9.9 ms/op`:

```text
speedup = 31.2 / 9.9 = 3.1515... = 3.15x
delta   = (9.9 - 31.2) / 31.2 = -68.27%
```

Go reports allocation bytes as `B/op`. The comparator displays decimal units,
so `2,289,213 B/op` becomes `2.289 MB/op`. A 31% reduction can only be claimed
when the raw baseline byte count is available. Similarly, `10,295 allocs/op`
is a 90% reduction only when the matching baseline is `102,950 allocs/op`.

`BenchmarkAsyncRecorderRecord` measures the steady-state `Record` path,
including normalization, synchronization, queueing, and any backpressure that
occurs during the run. A raw line containing
`910 ns/op 0 B/op 0 allocs/op` is `0.91 us/event`; both allocation metrics were
zero at Go benchmark reporting resolution. This is not proof that a rare or
one-time allocation can never occur. Recorder construction and shutdown are
excluded because the timer excludes those phases.

`B/op` is allocation traffic, not peak RSS and not retained heap. It is valid
for comparing allocation pressure, but it cannot by itself prove that memory
usage is bounded.

## What the cardinality test proves

`TestReportBuilderDefaultLimitsBoundHighCardinality` feeds 10,000 unique tool,
client, and session labels into the same builder. It checks the retained maps
directly and verifies that they stop at the configured limits:

| Dimension | Named groups retained | Overflow group |
|---|---:|---:|
| Tools | 128 | 1 |
| Clients | 256 | 1 |
| Sessions | 256 | 1 |
| Transports observed by this test | 1 | 0 |

Additional labels are aggregated into one `other` group. This establishes that
the report builder's grouping state does not grow without bound as label
cardinality rises. It does not claim constant memory for an arbitrarily large
input byte stream, JSONL file, caller-owned event slice, or output retention
outside the builder. Transport values are validated separately to the finite
production domain (`stdio` or `http`); their configured defensive cap is 16.

## Collect profiles separately

Profiles perturb timing, so collect them in a separate diagnostic run rather
than using that run as a before/after result:

```sh
# Allocation/retained-heap diagnostic run.
BENCH_COUNT=1 BENCH_TIME=3s BENCH_WARMUP=0 \
BENCH_REGEX='^BenchmarkReportBuilderHighCardinality$' \
BENCH_MEMPROFILE=/tmp/gokg-memory-profile/telemetry.mem \
  ./scripts/bench-telemetry.sh /tmp/gokg-memory-profile/benchmark.txt

go tool pprof -top -alloc_space /tmp/gokg-memory-profile/telemetry.mem
go tool pprof -top -inuse_space /tmp/gokg-memory-profile/telemetry.mem

# Separate CPU diagnostic run.
BENCH_COUNT=1 BENCH_TIME=3s BENCH_WARMUP=0 \
BENCH_REGEX='^BenchmarkReportBuilderHighCardinality$' \
BENCH_CPUPROFILE=/tmp/gokg-cpu-profile/telemetry.cpu \
  ./scripts/bench-telemetry.sh /tmp/gokg-cpu-profile/benchmark.txt

go tool pprof -top /tmp/gokg-cpu-profile/telemetry.cpu
```

`-alloc_space` locates allocation-traffic hot paths corresponding most closely
to `B/op`; `-inuse_space` inspects retained heap at the profile snapshot.

Use `go tool pprof -http=:0 <profile>` for interactive inspection when a local
browser is available.
