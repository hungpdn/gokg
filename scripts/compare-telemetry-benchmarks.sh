#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CALLER_DIR="$PWD"
GO="${GO:-go}"

usage() {
  cat <<'EOF'
Usage: scripts/compare-telemetry-benchmarks.sh <baseline.txt> <current.txt>

Both files must be raw outputs created by bench-telemetry.sh (or compatible
`go test -bench -benchmem` output). The report compares medians and does not
claim statistical significance.
EOF
}

absolute_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$CALLER_DIR" "$1" ;;
  esac
}

if [ "$#" -ne 2 ]; then
  usage >&2
  exit 2
fi

baseline="$(absolute_path "$1")"
current="$(absolute_path "$2")"

for input in "$baseline" "$current"; do
  if [ -e "${input}.lock" ]; then
    printf 'Benchmark result is locked by a running or interrupted capture: %s.lock\n' "$input" >&2
    exit 2
  fi
  if [ ! -f "$input" ]; then
    printf 'Benchmark result does not exist: %s\n' "$input" >&2
    exit 2
  fi
done

cd "$ROOT_DIR"
exec "$GO" run ./scripts/benchcmp "$baseline" "$current"
