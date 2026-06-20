# Changelog

All notable changes to GoKG will be documented in this file.

The format follows Keep a Changelog, and this project uses semantic versioning once tagged releases begin.

## [Unreleased]

### Added
- Go-native graph extraction for packages, files, folders, functions, methods, structs, interfaces, variables, constants, type aliases, channels, goroutines, boundaries, repositories, and workspaces.
- Relationship extraction for `CALLS`, `IMPORTS`, `CONTAINS`, `REFERENCES`, `INSTANTIATES`, `IMPLEMENTS`, `SPAWNS`, `SENDS_TO`, and `RECEIVES_FROM`.
- Strict read-only Cypher subset for graph exploration through CLI and MCP.
- MCP server over stdio and local HTTP.
- Multi-repo workspace support.
- Graph export as JSON, Mermaid, and DOT.
- Version metadata command with `gokg version` and `gokg version --json`.
- GitHub Actions CI across Linux, macOS, and Windows.
- GoReleaser configuration for tagged GitHub Releases.

### Changed
- Release builds inject version, commit, and build date metadata.
- Documentation now includes public install, quickstart, baseline, and release process sections.

### Security
- HTTP MCP mode is documented as local/trusted-client oriented and should not be exposed publicly without an external trusted network layer.

## [0.1.0-alpha.1] - Planned

Initial public alpha release.
