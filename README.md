# GoKG - Golang Knowledge Graph

[![Go Reference](https://pkg.go.dev/badge/github.com/hungpdn/gokg/gokg.svg)](https://pkg.go.dev/github.com/hungpdn/gokg)
![Go Version](https://img.shields.io/badge/go-1.25.12-blue)
[![Go CI](https://github.com/hungpdn/gokg/actions/workflows/go.yml/badge.svg)](https://github.com/hungpdn/gokg/actions/workflows/go.yml)
[![Release](https://github.com/hungpdn/gokg/actions/workflows/release.yml/badge.svg)](https://github.com/hungpdn/gokg/actions/workflows/release.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**GoKG** is a local, open-source tool that converts Go source code into a semantic knowledge graph. It acts as a local MCP server that gives AI coding agents deep architectural context.

Unlike generic Tree-sitter-based tools, GoKG uses Go-native analysis to understand packages, files, structs, interfaces, functions, methods, goroutines, channels, cross-package references, and multi-repo workspaces.

---

## Why it matters?

1. **Go toolchain-aware semantics**: Uses `go/packages`, `go/ast`, and `go/types` to resolve package identities, method receivers, type references, and implicit interface implementations.
2. **Concurrency-aware relationships**: Models goroutine spawning and channel send/receive relationships alongside calls, imports, and references.
3. **Multi-repository context**: Combines per-repository graphs into one workspace query surface and preserves resolvable cross-repository edges.
4. **Local & Pure Go**: Runs as a static Go binary with embedded BadgerDB storage. No hosted service, external graph database, embedding model, or API key is required.

GoKG is intentionally focused on Go. Choose a polyglot or visualization-first tool when language breadth is the priority; choose GoKG when Go semantics, concurrency, and architectural impact are the primary questions.

---

## Key Features

- **Go-native semantic parsing**: Extracts packages, files, folders, structs, interfaces, functions, methods, variables, channels, goroutines, HTTP routes, external boundaries, repos, and workspaces.
- **HTTP route topology**: Detects static `net/http` `Handle`/`HandleFunc` registrations plus Gin route/static registrations, including static Gin group prefixes and middleware, and links routes to their registrars and handlers.
- **Semantic relationships**: Maps `CALLS`, `IMPORTS`, `CONTAINS`, `REFERENCES`, `INSTANTIATES`, `IMPLEMENTS`, `SPAWNS`, `SENDS_TO`, `RECEIVES_FROM`, and `REGISTERS_ROUTE`.
- **Cypher query engine**: Runs a strict Neo4j-inspired Cypher subset so AI agents can build custom graph queries safely.
- **MCP server for AI agents**: Serves JSON-RPC 2.0 over `stdio` and `HTTP` for IDEs and coding agents.
- **Change impact analysis**: Maps Git diffs to graph nodes and reports inbound dependency impact for local repos and multi-repo workspaces.
- **Real-time incremental updates**: Optional file watcher reparses changed packages, refreshes repository structure, and merges updates into the live graph.
- **Multi-repo workspaces**: Merges multiple Go repositories into one graph while storing each repo in its own BadgerDB.
- **Graph statistics**: Reports node/edge/file counts, DB size, RAM estimate, node kinds, edge kinds, repo breakdowns, and top packages.
- **Visual export**: Exports the graph as `json`, `mermaid`, or `dot`.

---

## Installation

GoKG requires patched Go `1.25.12` or newer. With automatic toolchain selection enabled, `go.mod` selects Go `1.26.5`.

### Install with Go

```bash
go install github.com/hungpdn/gokg/cmd/gokg@latest
gokg version
```

Make sure your Go binary directory is on `PATH`. It is usually `$(go env GOPATH)/bin` or `$(go env GOBIN)` when `GOBIN` is set.

Pin a specific tagged version when you need reproducible installs:

```bash
go install github.com/hungpdn/gokg/cmd/gokg@<version>
```

`go install` builds report the installed module version from Go build information. Release binaries, Homebrew, and Scoop builds also include the exact commit and build date injected by GoReleaser.

### Install with Homebrew

```bash
brew tap hungpdn/tap
brew install --cask gokg
gokg version
```

### Install with Scoop

```powershell
scoop bucket add hungpdn https://github.com/hungpdn/scoop-bucket.git
scoop install hungpdn/gokg
gokg version
```

### Install from Release Binaries

Tagged GitHub Releases attach prebuilt binaries for:

| OS | Architectures | Package |
|---|---|---|
| macOS | `amd64`, `arm64` | `.tar.gz` |
| Linux | `amd64`, `arm64` | `.tar.gz` |
| Windows | `amd64` | `.zip` |

Each release also includes a SHA-256 checksum file.

### Build from Source

```bash
git clone https://github.com/hungpdn/gokg.git
cd gokg
make build
./bin/gokg version
```

---

## Quickstart

```bash
cd /path/to/your/go/project
gokg analyze --rebuild
gokg stats
gokg query 'MATCH (n:FUNC) RETURN n.Name, n.PkgPath LIMIT 10'
```

GoKG expects a loadable Go module or workspace. If `go list ./...` fails for a repository, fix that first or pass `--module` explicitly when the module prefix cannot be detected.

---

## Usage

### 1. Build the Knowledge Graph

```bash
cd /path/to/your/go/project
gokg analyze

# Rebuild from scratch
gokg analyze --db .gokg/ --rebuild
```

| Flag | Default | Description |
|---|---|---|
| `--module` | auto from `go.mod` | Module prefix for internal packages |
| `--db` | `.gokg/` | Path to BadgerDB directory |
| `--workspace` | empty | Workspace name for multi-repo analysis |
| `--rebuild` | `false` | Delete and rebuild the selected database while preserving the root telemetry file and its rotation segments |
| `--gc` | `true` | Run BadgerDB value-log GC after analysis |
| `--tests` | `false` | Include `_test.go` files in analysis |

### 2. Run the MCP Server

```bash
gokg mcp
gokg mcp --workspace my-platform
gokg mcp --http --addr 127.0.0.1:8080
gokg mcp --telemetry --telemetry-file .gokg/telemetry.jsonl
gokg mcp --telemetry --telemetry-max-bytes 67108864 --telemetry-max-backups 4
```

`gokg mcp --http` serves JSON-RPC over HTTP at `/mcp` by default and exposes a health check at `/healthz`. Use `--path` to change the MCP endpoint.

HTTP mode is intended for local trusted clients. It binds to `127.0.0.1` by default and does not add authentication, so avoid exposing it on a public interface unless another trusted network layer protects it.
Browser CORS access is limited to loopback origins such as `localhost`, `127.0.0.1`, and `[::1]`.

MCP telemetry is opt-in: `--telemetry-file`, `--telemetry-max-bytes`, and `--telemetry-max-backups` require `--telemetry`. The default active file is `.gokg/telemetry.jsonl`; it rotates at 64 MiB and retains four backups by default. Set `--telemetry-max-backups 0` to rotate without retaining a backup. Custom telemetry paths must be outside every database used by the command (including per-repository workspace databases), except for a root `<db>/telemetry.jsonl` file that GoKG knows how to preserve. Validation resolves existing symlink ancestors and filesystem aliases before applying this rule.

Recording is best-effort and never changes the MCP tool result. A bounded queue applies short backpressure when the writer falls behind: a record waits until it is enqueued, its short context expires, the recorder fails, or shutdown begins. Rejected and failed writes are counted by the async recorder, returned through recording/shutdown errors, and surfaced through rate-limited server warnings instead of being silently ignored. On shutdown, GoKG attempts to flush accepted records within a bounded five-second deadline before reporting an error. A non-blocking lock in the OS account's owner-validated `~/.cache/gokg/locks` remains outside the DB during destructive maintenance and enforces one writer per telemetry file for that OS user; concurrent MCP processes must use separate `--telemetry-file` paths. Unix containers that run with an arbitrary UID must provide a matching passwd entry/home directory. Cross-user and cross-container shared telemetry files are unsupported. Rotation bounds disk retention and removes segments beyond a newly lowered backup limit when the recorder starts, but does not replace an operator-defined retention or archival policy. GoKG requests owner-only file/directory modes on POSIX; on Windows or shared/custom directories, configure an appropriate filesystem ACL and grant telemetry write access to only one OS account.

Each event contains a schema version and timestamp, transport, tool name, success/error classification, microsecond latency, JSON payload byte counts, byte-based token estimates, and whether delivery of the MCP response failed. Stdio events may include the initialized client name/version and a random per-server session ID. HTTP telemetry is intentionally anonymous and stateless: `session_id`, client fields, and `user_agent` remain blank, and untrusted `Mcp-Session-Id` or `User-Agent` headers are not persisted. Raw tool arguments, tool responses, and source-code payloads are not recorded.

### 3. Run a Cypher Query

```bash
# Default local graph
gokg query 'MATCH (n:FUNC) WHERE n.PkgPath CONTAINS "parser" RETURN n.Name, n.ID LIMIT 20'

# Custom DB
gokg query --db /path/to/.gokg 'MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) RETURN s.Name, i.Name'

# Merged workspace graph
gokg query --workspace my-platform 'MATCH (n:FUNC) WHERE n.RepoID = "github.com/org/service-a" RETURN n.Name, n.PkgPath LIMIT 20'
```

### 4. Inspect Graph Statistics

```bash
# Human-readable local DB stats
gokg stats --db .gokg

# Workspace stats across all per-repo databases
gokg stats --workspace my-platform

# Machine-readable output for scripts/CI
gokg stats --db .gokg --json
```

`gokg stats` reports total nodes, edges, file nodes, unique source files, DB size, graph RAM estimate, current process heap allocation, nodes by kind, edges by kind, repo breakdowns, and the largest packages by node count.

### 5. Inspect MCP Telemetry

```bash
# Human-readable local MCP telemetry summary
gokg telemetry stats

# Machine-readable output for scripts
gokg telemetry stats --file .gokg/telemetry.jsonl --json

# Print the report and exit non-zero on delivery/data-quality diagnostics
gokg telemetry stats --strict
```

`gokg telemetry stats` opens and verifies a stable set of numeric rotation segments, reads each only to its snapshotted size from the oldest `.N` segment through `.1` and then the active file, and retries briefly if rotation changes that set. It groups records by tool, agent/client, session, and transport with bounded cardinality. Human and JSON reports include `latency_us`, MCP response delivery failures, a documented maximum relative error of 6.25% for p50/p95 histograms (max latency remains exact), and diagnostics for invalid or truncated lines/labels, scrubbed HTTP identity fields, legacy schema-v1 events, grouping-limit overflow, unsupported event versions, and numeric overflow. Schema-v1 events remain readable, but their HTTP identity fields are removed before grouping; `legacy_events` makes their older payload-byte semantics explicit and causes `--strict` to fail. Token counts are byte-based estimates for local usage analysis, not provider billing numbers. Without `--file`, a missing telemetry series produces an empty first-run report; an explicitly configured blank path or a series with neither active file nor numeric backup is an error. `--strict` still prints the report, then exits non-zero if delivery failures or data-quality diagnostics are present.

Reproduce telemetry latency, allocation, and bounded-cardinality claims with
the local workflow in [docs/telemetry-benchmarks.md](docs/telemetry-benchmarks.md).

`gokg analyze --rebuild` treats BadgerDB data as disposable but preserves the active root `telemetry.jsonl` file and rotation segments whose names start with `telemetry.jsonl.`. It acquires the same stable non-blocking telemetry lease and refuses to rebuild while an MCP writer is active; stop that MCP process before retrying.

### 6. Export Visual Graphs

```bash
gokg export --format mermaid --out graph.md
gokg export --format dot --out graph.dot
gokg export --format json --out graph.json
gokg export --workspace my-platform --format json --out workspace-graph.json
```

### 7. Analyze Change Impact

```bash
# Default: tracked staged + unstaged + untracked changes against HEAD
gokg impact

# Inspect second-hop dependents
gokg impact --depth 2

# Cap very large change sets
gokg impact --max-files 500 --max-nodes 200

# Only tracked staged + unstaged files
gokg impact --tracked-only

# Machine-readable report
gokg impact --json

# Fail CI unless graph freshness is fresh
gokg impact --strict-stale

# Workspace impact grouped by repo
gokg impact --workspace my-platform --base main
```

`gokg analyze` stores graph snapshot metadata such as analyzed time, repo root, module prefix, Git root, Git HEAD, dirty state, and whether `_test.go` files were included. `gokg impact` reads that metadata and reports graph freshness as `fresh`, `stale`, or `unknown` before listing changed and impacted nodes. Use `--strict-stale` in CI to exit non-zero unless freshness is `fresh`. Use `--max-files` and `--max-nodes` to cap large reports.

### 8. Multi-Repo Workspaces

```bash
gokg workspace init my-platform
gokg workspace add --workspace my-platform /path/to/service-a
gokg workspace add --workspace my-platform /path/to/service-b

gokg analyze --workspace my-platform --rebuild
gokg mcp --workspace my-platform

gokg workspace list
gokg workspace show my-platform
gokg workspace remove my-platform github.com/org/service-a
```

---

## MCP Tools for AI Agents

When connected through `gokg mcp`, GoKG exposes 12 tools:

| Tool | Description |
|---|---|
| `get_dependencies` | Nodes reached by dependency edges (`CALLS`, `IMPORTS`, `REFERENCES`, `INSTANTIATES`) |
| `get_blast_radius` | All nodes that depend on a given node |
| `get_concurrency_flow` | Goroutines and channels connected to a node |
| `get_concurrency_graph` | Goroutine/channel topology connected to a function |
| `get_implementations` | Structs implementing a given interface |
| `get_source_code` | Raw Go source for a node |
| `get_node_context` | Bounded source, dependency, dependent, location, route, interface, and concurrency context for a node |
| `get_repository_structure` | Repository folder/package/file tree from the graph |
| `get_change_impact` | Git diff to changed graph nodes and dependency impact |
| `find_path` | Shortest call path between two nodes |
| `search_nodes` | Find nodes by name or ID substring |
| `execute_cypher` | Run strict read-only Cypher queries against the graph |

For impact-driven work, call `get_change_impact` first, then call `get_node_context` on changed or impacted node IDs before deciding what to edit or test. `get_node_context` caps source, dependent, and relation payloads by default; tune `max_dependents`, `max_dependencies`, `max_relations`, `max_source_lines`, and `max_source_bytes` when a client needs more context. `max_callers` remains available as a deprecated alias for `max_dependents`.

---

## Cypher Query Engine

GoKG includes a lightweight Cypher subset for read-only graph exploration.

```cypher
MATCH <pattern> [WHERE <conditions>] RETURN <items> [LIMIT <positive n>]
```

**Node types:** `PACKAGE`, `FILE`, `FOLDER`, `FUNC`, `METHOD`, `CONSTANT`, `VARIABLE`, `TYPE_ALIAS`, `STRUCT`, `INTERFACE`, `CHANNEL`, `GOROUTINE`, `ROUTE`, `BOUNDARY`, `REPO`, `WORKSPACE`

**Edge types:** `CALLS`, `CONTAINS`, `IMPORTS`, `REFERENCES`, `INSTANTIATES`, `IMPLEMENTS`, `SPAWNS`, `SENDS_TO`, `RECEIVES_FROM`, `REGISTERS_ROUTE`

**Node properties:** `Name`, `ID`, `PkgPath`, `FilePath`, `Type`, `RepoID`

**Edge properties:** `Type`, `From`, `To`, `RepoID`

**WHERE operators:** `=`, `!=`, `CONTAINS`, plus `AND` between conditions.

Validation is strict: unknown aliases, node/edge types, properties, and trailing tokens return errors instead of silently broadening the query.
The CLI accepts queries without `LIMIT`, but MCP `execute_cypher` calls require a positive `LIMIT` to avoid unbounded responses.

Examples:

```cypher
MATCH (n:FUNC) WHERE n.PkgPath CONTAINS "parser" RETURN n.Name, n.ID LIMIT 20
MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = "Analyze" AND b.Type != "BOUNDARY" RETURN b.Name, b.Type LIMIT 20
MATCH (caller)-[r:CALLS]->(target:FUNC) WHERE target.Name = "AddEdge" RETURN caller.Name, caller.ID LIMIT 30
MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) WHERE i.Name = "Storage" RETURN s.Name, s.PkgPath
MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) RETURN f.Name, c.Name
MATCH (owner)-[r:REGISTERS_ROUTE]->(route:ROUTE) RETURN owner.Name, route.Name, route.FilePath LIMIT 50
MATCH (route:ROUTE)-[r:REFERENCES]->(handler) RETURN route.Name, handler.Name LIMIT 50
MATCH (a)-[r]-(b) WHERE a.Name = "worker" RETURN a.Name, r.Type, b.Name, b.Type LIMIT 30
```

Full reference: [docs/cypher-reference.md](docs/cypher-reference.md)

Run `gokg analyze --rebuild` after upgrading an existing database to populate route nodes.

---

## Integrating with AI Agents

Because GoKG exposes an MCP server over standard stdio and HTTP, you can connect it to AI clients.

For `stdio`
```json
// for each repo
{
  "mcpServers": {
    "gokg": {
      "command": "gokg",
      "args": ["mcp"]
    }
  }
}

// for workspace
{
  "mcpServers": {
    "gokg": {
      "command": "gokg",
      "args": ["mcp", "--workspace", "<your-workspace>"]
    }
  }
}
```

For `HTTP` clients that need a stdio bridge, start GoKG separately with `gokg mcp --http`, then pin the bridge package version in your client config:
```json
// for each repo
{
  "mcpServers": {
    "gokg": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote@<pinned-version>",
        "http://127.0.0.1:8080/mcp"
      ]
    }
  }
}

// for workspace, start gokg with:
// gokg mcp --workspace <your-workspace> --http --addr 127.0.0.1:8080
{
  "mcpServers": {
    "gokg": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote@<pinned-version>",
        "http://127.0.0.1:8080/mcp"
      ]
    }
  }
}
```

---

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md), [SECURITY.md](SECURITY.md), [CHANGELOG.md](CHANGELOG.md), and [docs/release.md](docs/release.md).

## License

MIT License. See [LICENSE](LICENSE) for details.
