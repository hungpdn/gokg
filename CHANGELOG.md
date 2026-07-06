# Changelog

All notable changes to GoKG will be documented in this file.

The format follows Keep a Changelog, and this project uses semantic versioning once tagged releases begin.

## [Unreleased]

### Added

- `get_node_context` MCP tool and graph query support for retrieving a node's full structural, source, dependency, dependent, routing, and concurrency context.
- `gokg impact` command for Git diff based change impact analysis with Markdown and JSON output.
- `get_change_impact` MCP tool for agent-driven impact reports across local repositories and workspaces.
- Graph query support for mapping changed file ranges to graph nodes and bounded inbound dependency blast-radius traversal.
- Impact analysis now reports graph freshness diagnostics to warn when the graph is stale relative to the current Git HEAD, dirty state, or CLI flags.
- `make security` target for running `govulncheck ./...`.

### Changed

- `get_node_context` now bounds dependent, relation, source-line, and source-byte payloads and exposes `max_dependents`, `max_relations`, `max_source_lines`, and `max_source_bytes` controls.
- MCP tool handling is split into dedicated tool definitions and handlers.
- MCP HTTP and stdio request contexts now propagate into long-running tool calls such as change impact analysis.
- Change impact analysis now includes untracked Git files by default, with `--tracked-only` available for tracked-only reports.
- Change impact analysis now caps changed-file processing with configurable `--max-files` and MCP `max_files` limits.
- README documents change impact analysis and the expanded MCP tool list.

### Fixed

- `get_node_context` now truncates oversized source and relationship sections with warnings instead of returning unbounded MCP responses.
- Hardened impact `base_ref` validation against Git option injection and control characters.
- Impact now verifies `base_ref` as a commit before running Git diff and separates revisions from pathspecs.
- Impact diff and untracked-file parsing now handle long generated lines and NUL-separated file names.
- Impact freshness diagnostics now compare current dirty status fingerprints, resolve symlink-equivalent roots, and make strict mode reject unknown freshness.
- Improved repo-aware file-range matching with normalized paths, symlink handling, and legacy blank `RepoID` compatibility.
- Single-repo impact and MCP mode now infer the analyzed repo root from graph metadata when available, avoiding custom `--db` and current-working-directory mismatches.
- Impact reports now treat no-hunk Git changes as whole-file changes and avoid rescanning the full graph for unmatched-file warnings.
- Impact Markdown output now escapes inline values such as repo IDs, node names, file paths, and base refs.

## [v0.1.0-alpha.4] - 2026-06-26

### Changed

- Release binaries now report `v`-prefixed semantic versions to match Git tags and `go install` builds.

### Fixed

- `gokg version` now uses Go build information as a fallback so binaries installed with `go install github.com/hungpdn/gokg/cmd/gokg@<version>` report the installed module version instead of `dev`.

## [v0.1.0-alpha.3] - 2026-06-26

### Added

- HTTP route graph support with `ROUTE` nodes and `REGISTERS_ROUTE` edges.
- Static `net/http` route extraction for `Handle` and `HandleFunc` registrations.
- Gin route extraction for router methods, static registrations, route groups, and static middleware prefixes.
- `get_repository_structure` MCP tool for repository folder/package/file tree snapshots.
- Repository structure graph management that refreshes during analysis and watcher updates.

### Changed

- Repository structure snapshots are now bounded for MCP responses to keep agent output predictable.
- Cypher and MCP docs now describe route nodes, route edges, and repository structure queries.
- README now includes a clearer "Why it matters?" section and route-aware feature summary.
- Raised the minimum Go toolchain to `1.25.11` to include current standard-library security fixes.
- Updated GitHub Actions and Go tooling dependencies.
- Added `govulncheck` gates to CI, the release workflow, and the release checklist.

### Fixed

- Prevented parser stack overflows on recursive Go type graphs.
- Broadened HTTP route extraction so more `net/http` and Gin registration patterns are captured consistently.
- Hardened watcher synchronization and repository structure refresh behavior.
- Prevented non-Go file removals from triggering full-repository reparses and ignored removal events for skipped files.

## [v0.1.0-alpha.2] - 2026-06-23

### Added

- MCP protocol version negotiation for broader client compatibility.
- Homebrew Cask and Scoop bucket publishing configuration through GoReleaser.
- Linting support and CI/tooling updates, including `golangci-lint` configuration and related Makefile targets.
- Additional regression coverage for MCP `execute_cypher`, JSON-RPC validation, Cypher parsing, graph queries, parser type references, watcher behavior, CLI cancellation, and BadgerDB rebuild validation.

### Changed

- MCP `execute_cypher` now requires a positive `LIMIT` to protect agent clients from unbounded result sets. CLI `gokg query` remains unchanged.
- File watching now skips hidden directories and common noisy trees such as `vendor`, `testdata`, and `node_modules`.
- Parser type dependency tracking now follows struct fields, embedded interfaces, and interface method signatures.
- Graph query evaluation and statistics reporting were tightened for more predictable results.
- Workspace listing now returns names in stable sorted order.
- README and Cypher reference docs were updated for the current MCP HTTP bridge, LIMIT behavior, and public-repo guidance.

### Fixed

- Prevented `--rebuild --db` from deleting arbitrary non-Badger directories by validating BadgerDB markers before removal.
- Rejected invalid JSON-RPC protocol versions in the MCP server.
- Restricted HTTP MCP origin checks to loopback origins.
- Propagated signal-aware root context through CLI commands and storage shutdown paths.
- Consolidated variable node type handling so parser, graph, MCP, and documentation agree.
- Fixed Cypher edge cases around positive `LIMIT`, duplicate `IMPLEMENTS` edges, and source-code line range validation.
- Made MCP source-code Markdown output safe for code containing embedded backtick fences.

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
