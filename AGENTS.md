# Repository Guidelines

## Project Structure & Module Organization

GoKG is a Go CLI and library module (`github.com/hungpdn/gokg`). CLI entry points and Cobra commands live in `cmd/gokg/`. Core packages live under `internal/`: `parser` extracts Go entities, `graph` stores/query exports relationships, `cypher` parses the query subset, `storage` wraps BadgerDB, `mcp` serves JSON-RPC, and `workspace`/`watcher` handle multi-repo and incremental flows. Tests are colocated with code as `*_test.go`. User-facing docs belong in `README.md` or `docs/`; generated/local data such as `.gokg/`, `coverage.out`, and `coverage.html` should not be treated as source.

## Build, Test, and Development Commands

- `make build`: builds `bin/gokg` with release-style linker metadata.
- `make build-debug`: builds an unstripped debug binary.
- `make test`: runs `go test -v ./...`.
- `make test-race`: runs the full suite with the race detector.
- `make test-coverage`: writes `coverage.out` and `coverage.html`.
- `make format`: runs `go fmt ./...`.
- `make lint`: runs `golangci-lint run --timeout=5m ./...`.
- `go run ./cmd/gokg analyze --rebuild`: validates local graph generation.

Install pinned lint tooling with `make install-tools` when needed.

## Coding Style & Naming Conventions

Use standard Go formatting (`gofmt`/tabs) and keep package APIs small and explicit. Follow existing names: short lowercase package names, exported identifiers in `PascalCase`, unexported identifiers in `camelCase`, and tests named `TestXxx`. Prefer table-driven tests for parser, graph, Cypher, and CLI behavior. Keep command-line flags and help text consistent with existing Cobra commands.

## Testing Guidelines

The project uses Go’s `testing` package with `testify` where helpful. Add or update colocated `*_test.go` files for behavior changes. Run `make test` before opening a PR; use `make test-race` for concurrency, watcher, MCP server, or storage changes. For CLI or graph changes, include a focused `gokg analyze --rebuild` check or equivalent fixture-based test.

## Commit & Pull Request Guidelines

Recent history uses Conventional Commit prefixes such as `feat:`, `fix:`, `refactor:`, `test:`, and `docs:`. Keep subjects imperative and scoped to one change. PRs should include a summary, validation checklist, linked issue when applicable, and documentation updates for changed behavior or commands. CI runs lint, build, and tests on Ubuntu, macOS, and Windows.

## Security & Configuration Tips

Do not expose unauthenticated MCP HTTP mode beyond trusted loopback/network layers. Report suspected vulnerabilities through `SECURITY.md`, not public issues.
