# Changelog

All notable changes to GoKG will be documented in this file.

The format follows Keep a Changelog, and this project uses semantic versioning once tagged releases begin.

## [Unreleased]

### Added

### Changed

### Fixed

## [0.1.0-alpha.1] - 2026-06-21

### Added

- **Go-native semantic parsing**: Extracts packages, files, folders, structs, interfaces, functions, methods, variables, channels, goroutines, external boundaries, repos, and workspaces.
- **Semantic relationships**: Maps `CALLS`, `IMPORTS`, `CONTAINS`, `REFERENCES`, `INSTANTIATES`, `IMPLEMENTS`, `SPAWNS`, `SENDS_TO`, and `RECEIVES_FROM`.
- **Cypher query engine**: Runs a strict Neo4j-inspired Cypher subset so AI agents can build custom graph queries safely.
- **MCP server for AI agents**: Serves JSON-RPC 2.0 over `stdio` and `HTTP` for IDEs and coding agents.
- **Real-time incremental updates**: Optional file watcher reparses changed packages and merges updates into the live graph.
- **Multi-repo workspaces**: Merges multiple Go repositories into one graph while storing each repo in its own BadgerDB.
- **Graph statistics**: Reports node/edge/file counts, DB size, RAM estimate, node kinds, edge kinds, repo breakdowns, and top packages.
- **Visual export**: Exports the graph as `json`, `mermaid`, or `dot`.
