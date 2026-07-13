# Contributing to GoKG

First off, thank you for considering contributing to GoKG! It's people like you that make open source such a great community.

## How Can I Contribute?

### Reporting Bugs
*   Ensure the bug was not already reported by searching on GitHub under Issues.
*   If you're unable to find an open issue addressing the problem, open a new one. Be sure to include a title and clear description, as much relevant information as possible, and a code sample or an executable test case demonstrating the expected behavior that is not occurring.

### Suggesting Enhancements
*   Open a new issue with a clear title and description.
*   Explain why this enhancement would be useful to most GoKG users.

### Pull Requests
1. Fork the repo and create your branch from `main`.
2. If you've added code that should be tested, add tests.
3. If you've changed APIs, update the documentation.
4. Ensure the local quality gate passes (`make check`).
5. For release-related changes, run the heavier gate and `goreleaser check` if GoReleaser is installed.
6. Open the pull request and fill out the checklist.

## Local Development Setup
1. Clone your fork locally.
2. Run `go mod tidy` to download dependencies.
3. Install pinned development tools with `make install-tools` when you plan to run lint locally.
4. Make your changes in the `internal/` or `cmd/` directories.
5. Run the default verification gate with `make check`.
6. For a deeper local pass, run `RUN_RACE=1 RUN_LINT=1 RUN_VULN=1 SMOKE_TESTS=1 ./scripts/check.sh`.

`scripts/check.sh` verifies formatting, `go vet`, unit tests, buildability, and a real CLI smoke test for `analyze`, `stats`, and `query` using a temporary database. Temporary files are cleaned up automatically when the script exits.

For telemetry performance work, use `make bench-telemetry` and preserve both
the raw result and its `.meta` file. See
[docs/telemetry-benchmarks.md](docs/telemetry-benchmarks.md) for fair
before/after capture, comparison, profiling, and bounded-cardinality checks.

## Code Style
We follow standard Go formatting conventions. Please run `go fmt ./...` before committing. CI runs `golangci-lint` v2.12.2; use `make install-tools` and `make lint` to run the same linter locally.

## Security Reports
Please do not open public issues for suspected vulnerabilities. See [SECURITY.md](SECURITY.md) for the reporting process.

By contributing, you agree that your contributions will be licensed under its MIT License.
