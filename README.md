# GoKG - Golang Knowledge Graph

[![Go Reference](https://pkg.go.dev/badge/github.com/hungpdn/gokg/cmd/gokg.svg)](https://pkg.go.dev/github.com/hungpdn/gokg/cmd/gokg)
![Go Version](https://img.shields.io/badge/go-1.25.11-blue)
[![Go CI](https://github.com/hungpdn/gokg/actions/workflows/go.yml/badge.svg)](https://github.com/hungpdn/gokg/actions/workflows/go.yml)
[![Release](https://github.com/hungpdn/gokg/actions/workflows/release.yml/badge.svg)](https://github.com/hungpdn/gokg/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/hungpdn/gokg)](https://goreportcard.com/report/github.com/hungpdn/gokg)
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

GoKG requires Go `1.25.11` or newer.

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
| `--rebuild` | `false` | Delete and rebuild the selected database |
| `--gc` | `true` | Run BadgerDB value-log GC after analysis |
| `--tests` | `false` | Include `_test.go` files in analysis |

### 2. Run the MCP Server

```bash
gokg mcp
gokg mcp --workspace my-platform
gokg mcp --http --addr 127.0.0.1:8080
```

`gokg mcp --http` serves JSON-RPC over HTTP at `/mcp` by default and exposes a health check at `/healthz`. Use `--path` to change the MCP endpoint.

HTTP mode is intended for local trusted clients. It binds to `127.0.0.1` by default and does not add authentication, so avoid exposing it on a public interface unless another trusted network layer protects it.
Browser CORS access is limited to loopback origins such as `localhost`, `127.0.0.1`, and `[::1]`.

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

### 5. Export Visual Graphs

```bash
gokg export --format mermaid --out graph.md
gokg export --format dot --out graph.dot
gokg export --format json --out graph.json
gokg export --workspace my-platform --format json --out workspace-graph.json
```

### 6. Analyze Change Impact

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

# Workspace impact grouped by repo
gokg impact --workspace my-platform --base main
```

`gokg impact` reads existing graph databases; run `gokg analyze --rebuild` first when the graph is stale. It maps changed Git hunks to graph nodes, reports changed nodes, and lists dependency nodes that depend on them. Use `--max-files` and `--max-nodes` to cap large reports.

### 7. Multi-Repo Workspaces

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

When connected through `gokg mcp`, GoKG exposes 11 tools:

| Tool | Description |
|---|---|
| `get_dependencies` | Nodes reached by dependency edges (`CALLS`, `IMPORTS`, `REFERENCES`, `INSTANTIATES`) |
| `get_blast_radius` | All nodes that depend on a given node |
| `get_concurrency_flow` | Goroutines and channels connected to a node |
| `get_concurrency_graph` | Goroutine/channel topology connected to a function |
| `get_implementations` | Structs implementing a given interface |
| `get_source_code` | Raw Go source for a node |
| `get_repository_structure` | Repository folder/package/file tree from the graph |
| `get_change_impact` | Git diff to changed graph nodes and dependency impact |
| `find_path` | Shortest call path between two nodes |
| `search_nodes` | Find nodes by name or ID substring |
| `execute_cypher` | Run strict read-only Cypher queries against the graph |

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
