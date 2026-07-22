#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CALLER_DIR="$PWD"

GO="${GO:-go}"
BENCH_COUNT="${BENCH_COUNT:-10}"
BENCH_TIME="${BENCH_TIME:-1s}"
BENCH_CPU="${BENCH_CPU:-}"
BENCH_WARMUP="${BENCH_WARMUP:-1}"
BENCH_CHECK_BOUNDS="${BENCH_CHECK_BOUNDS:-1}"
BENCH_REGEX="${BENCH_REGEX:-^(BenchmarkReportBuilderHighCardinality|BenchmarkAsyncRecorderRecord|BenchmarkAsyncRecorderRecordParallel|BenchmarkBuildReportFromReader)$}"
BENCH_OUTPUT="${1:-${BENCH_OUTPUT:-}}"
BENCH_MEMPROFILE="${BENCH_MEMPROFILE:-}"
BENCH_CPUPROFILE="${BENCH_CPUPROFILE:-}"

usage() {
  cat <<'EOF'
Usage: scripts/bench-telemetry.sh [output-file]

Environment variables:
  BENCH_OUTPUT        Raw Go benchmark output (default: /tmp/gokg-telemetry-<UTC>.txt)
  BENCH_COUNT         Number of independent samples (default: 10)
  BENCH_TIME          Minimum duration per benchmark sample (default: 1s)
  BENCH_CPU           One positive GOMAXPROCS value; empty uses Go's default
  BENCH_WARMUP        Run a short discarded warm-up first (default: 1)
  BENCH_CHECK_BOUNDS  Run the high-cardinality bound regression test (default: 1)
  BENCH_REGEX         Override the selected telemetry benchmarks
  BENCH_MEMPROFILE    Optional heap profile path; use only for a separate profiling run
  BENCH_CPUPROFILE    Optional CPU profile path; use only for a separate profiling run
  GO                  Go executable (default: go)

The script writes machine/revision metadata beside the raw result as <output-file>.meta.
EOF
}

is_enabled() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$CALLER_DIR" "$1" ;;
  esac
}

print_command() {
  printf '+' >&2
  printf ' %q' "$@" >&2
  printf '\n' >&2
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

case "$BENCH_COUNT" in
  ''|*[!0-9]*)
    printf 'BENCH_COUNT must be a positive integer, got %q.\n' "$BENCH_COUNT" >&2
    exit 2
    ;;
esac
if [ "$BENCH_COUNT" -lt 1 ]; then
  printf 'BENCH_COUNT must be greater than zero.\n' >&2
  exit 2
fi

if [ -z "$BENCH_REGEX" ]; then
  printf 'BENCH_REGEX must not be empty.\n' >&2
  exit 2
fi

if [ -n "$BENCH_CPU" ]; then
  case "$BENCH_CPU" in
    *[!0-9]*)
      printf 'BENCH_CPU must be one positive integer, got %q.\n' "$BENCH_CPU" >&2
      exit 2
      ;;
  esac
  if [ "$BENCH_CPU" -lt 1 ]; then
    printf 'BENCH_CPU must be greater than zero.\n' >&2
    exit 2
  fi
fi

cd "$ROOT_DIR"
benchmark_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
git_commit="unknown"
git_dirty="unknown"
benchmark_harness_version="telemetry-v1"
benchmark_harness_hash="unknown"
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git_commit="$(git rev-parse HEAD)"
  if git diff --quiet && git diff --cached --quiet && [ -z "$(git ls-files --others --exclude-standard)" ]; then
    git_dirty="false"
  else
    git_dirty="true"
  fi
  benchmark_harness_hash="$(git hash-object internal/telemetry/telemetry_test.go)"
fi
go_version="$("$GO" version)"
goos="$("$GO" env GOOS)"
goarch="$("$GO" env GOARCH)"
uname_value="$(uname -a)"

default_output_dir=""
if [ -z "$BENCH_OUTPUT" ]; then
  default_output_dir="$(mktemp -d "${TMPDIR:-/tmp}/gokg-telemetry.XXXXXX")"
  BENCH_OUTPUT="$default_output_dir/benchmark.txt"
fi
BENCH_OUTPUT="$(absolute_path "$BENCH_OUTPUT")"
metadata_file="${BENCH_OUTPUT}.meta"
lock_dir="${BENCH_OUTPUT}.lock"
lock_acquired=0
raw_tmp=""
metadata_tmp=""
cleanup() {
  status=$?
  trap - EXIT
  if [ -n "$raw_tmp" ]; then
    rm -f "$raw_tmp"
  fi
  if [ -n "$metadata_tmp" ]; then
    rm -f "$metadata_tmp"
  fi
  if [ "$lock_acquired" -eq 1 ]; then
    rmdir "$lock_dir" 2>/dev/null || true
  fi
  if [ -n "$default_output_dir" ] && [ ! -e "$BENCH_OUTPUT" ]; then
    rmdir "$default_output_dir" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT

if [ -n "$BENCH_MEMPROFILE" ]; then
  BENCH_MEMPROFILE="$(absolute_path "$BENCH_MEMPROFILE")"
fi
if [ -n "$BENCH_CPUPROFILE" ]; then
  BENCH_CPUPROFILE="$(absolute_path "$BENCH_CPUPROFILE")"
fi

if [ -n "$BENCH_MEMPROFILE" ] && [ "$BENCH_MEMPROFILE" = "$BENCH_CPUPROFILE" ]; then
  printf 'BENCH_MEMPROFILE and BENCH_CPUPROFILE must use different paths.\n' >&2
  exit 2
fi
for profile_path in "$BENCH_MEMPROFILE" "$BENCH_CPUPROFILE"; do
  if [ -n "$profile_path" ] && { [ "$profile_path" = "$BENCH_OUTPUT" ] || [ "$profile_path" = "$metadata_file" ]; }; then
    printf 'Profile paths must differ from the benchmark output and metadata paths.\n' >&2
    exit 2
  fi
done

profile_output_dir=""
profile_binary=""
if [ -n "$BENCH_MEMPROFILE" ]; then
  profile_output_dir="$(dirname "$BENCH_MEMPROFILE")"
elif [ -n "$BENCH_CPUPROFILE" ]; then
  profile_output_dir="$(dirname "$BENCH_CPUPROFILE")"
fi
if [ -n "$profile_output_dir" ]; then
  profile_binary="$profile_output_dir/telemetry.test"
  for reserved_path in "$BENCH_OUTPUT" "$metadata_file" "$BENCH_MEMPROFILE" "$BENCH_CPUPROFILE"; do
    if [ -n "$reserved_path" ] && [ "$reserved_path" = "$profile_binary" ]; then
      printf 'Benchmark output and profile paths must not use reserved test binary path %s.\n' "$profile_binary" >&2
      exit 2
    fi
  done
fi

mkdir -p "$(dirname "$BENCH_OUTPUT")"
if [ -n "$BENCH_MEMPROFILE" ]; then
  mkdir -p "$(dirname "$BENCH_MEMPROFILE")"
fi
if [ -n "$BENCH_CPUPROFILE" ]; then
  mkdir -p "$(dirname "$BENCH_CPUPROFILE")"
fi

if ! mkdir "$lock_dir" 2>/dev/null; then
  printf 'Another benchmark run may own the output lock: %s\n' "$lock_dir" >&2
  exit 2
fi
lock_acquired=1

if [ -e "$BENCH_OUTPUT" ] || [ -L "$BENCH_OUTPUT" ] || [ -e "$metadata_file" ] || [ -L "$metadata_file" ]; then
  printf 'Refusing to overwrite an existing benchmark result or metadata file: %s\n' "$BENCH_OUTPUT" >&2
  exit 2
fi

raw_tmp="$(mktemp "${BENCH_OUTPUT}.tmp.XXXXXX")"
metadata_tmp="$(mktemp "${metadata_file}.tmp.XXXXXX")"

if is_enabled "$BENCH_CHECK_BOUNDS"; then
  printf '\n==> Verify bounded high-cardinality aggregation\n' >&2
  bounds_command=(
    "$GO" test
    -run '^TestReportBuilderDefaultLimitsBoundHighCardinality$'
    -count 1
    ./internal/telemetry
  )
  print_command "${bounds_command[@]}"
  "${bounds_command[@]}"
fi

common_args=(
  -run '^$'
  -bench "$BENCH_REGEX"
)
if [ -n "$BENCH_CPU" ]; then
  common_args+=(-cpu "$BENCH_CPU")
fi

if is_enabled "$BENCH_WARMUP"; then
  printf '\n==> Warm up benchmark code paths (result discarded)\n' >&2
  warmup_command=(
    "$GO" test
    "${common_args[@]}"
    -benchtime 100ms
    -count 1
    ./internal/telemetry
  )
  print_command "${warmup_command[@]}"
  "${warmup_command[@]}" >/dev/null
fi

bench_command=(
  "$GO" test
  "${common_args[@]}"
  -benchmem
  -benchtime "$BENCH_TIME"
  -count "$BENCH_COUNT"
)
if [ -n "$BENCH_MEMPROFILE" ]; then
  bench_command+=(-memprofile "$BENCH_MEMPROFILE")
fi
if [ -n "$BENCH_CPUPROFILE" ]; then
  bench_command+=(-cpuprofile "$BENCH_CPUPROFILE")
fi
if [ -n "$profile_output_dir" ]; then
  bench_command+=(-outputdir "$profile_output_dir")
  bench_command+=(-o "$profile_binary")
fi
bench_command+=(./internal/telemetry)

printf '\n==> Capture telemetry benchmark samples\n' >&2
print_command "${bench_command[@]}"
"${bench_command[@]}" | tee "$raw_tmp"

if ! grep -q '^Benchmark' "$raw_tmp" || ! grep -qx 'PASS' "$raw_tmp"; then
  printf 'Benchmark output is incomplete or contains no benchmark samples; result was not published.\n' >&2
  exit 1
fi

cpu_model="$(awk -F ': ' '$1 == "cpu" { print substr($0, index($0, $2)); exit }' "$raw_tmp")"
{
  printf 'timestamp_utc=%s\n' "$benchmark_started_at"
  printf 'repository=%s\n' "$ROOT_DIR"
  printf 'go_version=%s\n' "$go_version"
  printf 'goos=%s\n' "$goos"
  printf 'goarch=%s\n' "$goarch"
  printf 'cpu_model=%s\n' "${cpu_model:-unknown}"
  printf 'uname=%s\n' "$uname_value"
  printf 'git_commit=%s\n' "$git_commit"
  printf 'git_dirty=%s\n' "$git_dirty"
  printf 'benchmark_harness_version=%s\n' "$benchmark_harness_version"
  printf 'benchmark_harness_hash=%s\n' "$benchmark_harness_hash"
  printf 'bench_count=%s\n' "$BENCH_COUNT"
  printf 'bench_time=%s\n' "$BENCH_TIME"
  printf 'bench_cpu=%s\n' "${BENCH_CPU:-go-default}"
  printf 'bench_regex=%s\n' "$BENCH_REGEX"
  printf 'memprofile=%s\n' "${BENCH_MEMPROFILE:-disabled}"
  printf 'cpuprofile=%s\n' "${BENCH_CPUPROFILE:-disabled}"
  printf 'command='
  printf ' %q' "${bench_command[@]}"
  printf '\n'
} > "$metadata_tmp"

mv -f "$metadata_tmp" "$metadata_file"
metadata_tmp=""
if ! mv -f "$raw_tmp" "$BENCH_OUTPUT"; then
  rm -f "$metadata_file"
  exit 1
fi
raw_tmp=""

printf '\nRaw benchmark: %s\n' "$BENCH_OUTPUT" >&2
printf 'Run metadata:  %s\n' "$metadata_file" >&2
if [ -n "$BENCH_MEMPROFILE" ]; then
  printf 'Heap profile:  %s\n' "$BENCH_MEMPROFILE" >&2
fi
if [ -n "$BENCH_CPUPROFILE" ]; then
  printf 'CPU profile:   %s\n' "$BENCH_CPUPROFILE" >&2
fi
if [ -n "$profile_output_dir" ]; then
  printf 'Test binary:   %s\n' "$profile_binary" >&2
fi
