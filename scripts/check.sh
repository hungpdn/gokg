#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GO="${GO:-go}"
RUN_RACE="${RUN_RACE:-0}"
RUN_LINT="${RUN_LINT:-0}"
RUN_VULN="${RUN_VULN:-0}"
RUN_SMOKE="${RUN_SMOKE:-1}"
SMOKE_TESTS="${SMOKE_TESTS:-0}"

CHECK_TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/gokg-check.XXXXXX")"
cleanup() {
  rm -rf "$CHECK_TMP_ROOT"
}
trap cleanup EXIT

if [ -z "${GOCACHE:-}" ]; then
  export GOCACHE="$CHECK_TMP_ROOT/gocache"
fi
mkdir -p "$GOCACHE"

step() {
  printf '\n==> %s\n' "$1"
}

run() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
  "$@"
}

run_to_file() {
  output_file="$1"
  shift
  printf '+'
  printf ' %q' "$@"
  printf ' > %q\n' "$output_file"
  if ! "$@" > "$output_file" 2>&1; then
    cat "$output_file" >&2
    return 1
  fi
}

is_enabled() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

step "Check formatting"
unformatted="$(
  find . \
    -path './.git' -prune -o \
    -path './.gokg' -prune -o \
    -path './bin' -prune -o \
    -path './dist' -prune -o \
    -name '*.go' -type f -print0 | xargs -0 gofmt -l
)"
if [ -n "$unformatted" ]; then
  printf 'The following Go files need gofmt:\n%s\n' "$unformatted" >&2
  exit 1
fi

step "Vet"
run "$GO" vet ./...

step "Unit tests"
run "$GO" test ./...

if is_enabled "$RUN_RACE"; then
  step "Race tests"
  run "$GO" test -race ./...
fi

step "Build"
run "$GO" build ./...

if is_enabled "$RUN_LINT"; then
  step "Lint"
  if ! command -v golangci-lint >/dev/null 2>&1; then
    printf 'golangci-lint is not installed. Run `make install-tools` first.\n' >&2
    exit 1
  fi
  run golangci-lint run --timeout=5m ./...
fi

if is_enabled "$RUN_VULN"; then
  step "Vulnerability scan"
  if ! command -v govulncheck >/dev/null 2>&1; then
    printf 'govulncheck is not installed. Run `go install golang.org/x/vuln/cmd/govulncheck@latest` first.\n' >&2
    exit 1
  fi
  run govulncheck ./...
fi

if is_enabled "$RUN_SMOKE"; then
  step "CLI smoke test"
  tmp_root="$CHECK_TMP_ROOT/smoke"
  mkdir -p "$tmp_root"

  smoke_bin="$tmp_root/gokg"
  smoke_db="$tmp_root/db"

  run "$GO" build -o "$smoke_bin" ./cmd/gokg

  analyze_args=(analyze --db "$smoke_db" --gc=false)
  if is_enabled "$SMOKE_TESTS"; then
    analyze_args+=(--tests)
  fi

  run "$smoke_bin" "${analyze_args[@]}"
  run_to_file "$tmp_root/stats.json" "$smoke_bin" stats --db "$smoke_db" --json
  run_to_file "$tmp_root/query.txt" "$smoke_bin" query --db "$smoke_db" 'MATCH (n) RETURN n LIMIT 1'
fi

printf '\nAll checks passed.\n'
