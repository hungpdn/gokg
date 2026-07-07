# Repository Guidelines

## Project Structure & Module Organization

GoKG is a Go CLI and library module (`github.com/hungpdn/gokg`). CLI commands live in `cmd/gokg/`. Core packages live under `internal/`: `parser` extracts Go entities, `graph` stores and queries relationships, `cypher` parses the supported query subset, `storage` wraps BadgerDB, `mcp` serves JSON-RPC, and `workspace`/`watcher` handle multi-repo and incremental flows. Tests are colocated as `*_test.go`. User-facing docs belong in `README.md` or `docs/`; generated data such as `.gokg/`, `coverage.out`, and `coverage.html` is local artifact output.

## Build, Test, and Development Commands

- `make build`: build `bin/gokg` with release-style linker metadata.
- `make build-debug`: build an unstripped debug binary.
- `make test`: run `go test -v ./...`.
- `make test-race`: run the full suite with the race detector.
- `make test-coverage`: write `coverage.out` and `coverage.html`.
- `make format`: run `go fmt ./...`.
- `make lint`: run `golangci-lint run --timeout=5m ./...`.
- `go run ./cmd/gokg analyze --rebuild`: validate local graph generation.

Install pinned tooling with `make install-tools` when running lint locally.

## Coding Style & Naming Conventions

Use standard Go formatting (`gofmt` tabs). Keep package APIs explicit and focused. Follow existing naming: short lowercase package names, exported identifiers in `PascalCase`, unexported identifiers in `camelCase`, tests named `TestXxx`. Prefer table-driven tests for parser, graph, Cypher, MCP, and CLI behavior.

## Testing Guidelines

Add or update colocated `*_test.go` files for behavior changes. Run focused package tests first, then `make test`; use `make test-race` for watcher, MCP, storage, or concurrency changes. For parser, graph, Cypher, or CLI changes, include a fixture or `gokg analyze --rebuild` validation path.

## Agent & Skill Workflow

Use the personal `gokg` skill (`~/.codex/skills/gokg`) when an agent needs architectural context, call graphs, dependency/blast-radius checks, interface implementations, concurrency flows, or MCP/Cypher guidance. For principal-engineer quality gates, prefer or create these skills under `${CODEX_HOME:-$HOME/.codex}/skills`: `go-principal-engineer`, `go-performance-profiler`, `go-security-production`, and `go-test-strategy`. Restart Codex after installing or editing skills.

## Commit & Pull Request Guidelines

Recent commits use Conventional Commit prefixes such as `feat:`, `fix:`, `refactor:`, `test:`, and `docs:`. Keep commits scoped and imperative. PRs should include summary, validation checklist, linked issue when relevant, and docs updates for changed behavior. CI runs lint, build, and tests on Ubuntu, macOS, and Windows.

## Security & Configuration Tips

Do not expose unauthenticated MCP HTTP mode beyond trusted loopback/network layers. Validate user-controlled Cypher, paths, and source-code ranges defensively. Report suspected vulnerabilities through `SECURITY.md`, not public issues.
