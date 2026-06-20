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
4. Ensure the test suite passes (`go test ./...`).
5. For release-related changes, run `goreleaser check` if GoReleaser is installed.
6. Open the pull request and fill out the checklist.

## Local Development Setup
1. Clone your fork locally.
2. Run `go mod tidy` to download dependencies.
3. Make your changes in the `internal/` or `cmd/` directories.
4. Run tests using `go test ./...`
5. Run the analyzer locally to test graph generation: `go run ./cmd/gokg analyze`.

## Code Style
We follow standard Go formatting conventions. Please run `go fmt ./...` before committing.

## Security Reports
Please do not open public issues for suspected vulnerabilities. See [SECURITY.md](SECURITY.md) for the reporting process.

By contributing, you agree that your contributions will be licensed under its MIT License.
