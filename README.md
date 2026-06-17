# GoKG - Golang Knowledge Graph

[![Go Report Card](https://goreportcard.com/badge/github.com/hungpdn/gokg)](https://goreportcard.com/report/github.com/hungpdn/gokg)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**GoKG** is a local, open-source tool that converts Go source code into a semantic knowledge graph. It acts as a local MCP server that gives AI coding agents deep architectural context.

Unlike generic Tree-sitter-based tools, GoKG uses Go-native analysis to understand packages, files, structs, interfaces, functions, methods, goroutines, channels, cross-package references, and multi-repo workspaces.

---

## Key Features

- **Go-native semantic parsing**: Extracts packages, files, folders, structs, interfaces, functions, methods, variables, channels, goroutines, external boundaries, repos, and workspaces.
- **Semantic relationships**: Maps `CALLS`, `IMPORTS`, `CONTAINS`, `IMPLEMENTS`, `SPAWNS`, `SENDS_TO`, and `RECEIVES_FROM`.
- **Cypher query engine**: Runs a strict Neo4j-inspired Cypher subset so AI agents can build custom graph queries safely.
- **MCP server for AI agents**: Serves JSON-RPC 2.0 over `stdio` for IDEs and coding agents.
- **Real-time incremental updates**: Optional file watcher reparses changed packages and merges updates into the live graph.
- **Multi-repo workspaces**: Merges multiple Go repositories into one graph while storing each repo in its own BadgerDB.
- **Graph statistics**: Reports node/edge/file counts, DB size, RAM estimate, node kinds, edge kinds, repo breakdowns, and top packages.
- **Visual export**: Exports the graph as `json`, `mermaid`, or `dot`.

---

## Installation

This module currently targets Go `1.25.0`.

```bash
git clone https://github.com/hungpdn/gokg.git
cd gokg
go install ./cmd/gokg
```

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

### 2. Run the MCP Server

```bash
gokg mcp
gokg mcp --workspace my-platform
```

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

### 6. Multi-Repo Workspaces

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

When connected through `gokg mcp`, GoKG exposes 9 tools:

| Tool | Description |
|---|---|
| `get_dependencies` | All nodes a function calls or imports |
| `get_blast_radius` | All nodes that depend on a given node |
| `get_concurrency_flow` | Goroutines and channels connected to a node |
| `get_concurrency_graph` | Goroutine/channel topology connected to a function |
| `get_implementations` | Structs implementing a given interface |
| `get_source_code` | Raw Go source for a node |
| `find_path` | Shortest call path between two nodes |
| `search_nodes` | Find nodes by name or ID substring |
| `execute_cypher` | Run strict read-only Cypher queries against the graph |

---

## Cypher Query Engine

GoKG includes a lightweight Cypher subset for read-only graph exploration.

```cypher
MATCH <pattern> [WHERE <conditions>] RETURN <items> [LIMIT <n>]
```

**Node types:** `PACKAGE`, `FILE`, `FOLDER`, `FUNC`, `METHOD`, `VAR`, `STRUCT`, `INTERFACE`, `CHANNEL`, `GOROUTINE`, `BOUNDARY`, `REPO`, `WORKSPACE`

**Edge types:** `CALLS`, `CONTAINS`, `IMPORTS`, `IMPLEMENTS`, `SPAWNS`, `SENDS_TO`, `RECEIVES_FROM`

**Node properties:** `Name`, `ID`, `PkgPath`, `FilePath`, `Type`, `RepoID`

**Edge properties:** `Type`, `From`, `To`, `RepoID`

**WHERE operators:** `=`, `!=`, `CONTAINS`, plus `AND` between conditions.

Validation is strict: unknown aliases, node/edge types, properties, and trailing tokens return errors instead of silently broadening the query.

Examples:

```cypher
MATCH (n:FUNC) WHERE n.PkgPath CONTAINS "parser" RETURN n.Name, n.ID LIMIT 20
MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = "Analyze" AND b.Type != "BOUNDARY" RETURN b.Name, b.Type LIMIT 20
MATCH (caller)-[r:CALLS]->(target:FUNC) WHERE target.Name = "AddEdge" RETURN caller.Name, caller.ID LIMIT 30
MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) WHERE i.Name = "Storage" RETURN s.Name, s.PkgPath
MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) RETURN f.Name, c.Name
MATCH (a)-[r]-(b) WHERE a.Name = "worker" RETURN a.Name, r.Type, b.Name, b.Type LIMIT 30
```

Full reference: [docs/cypher-reference.md](docs/cypher-reference.md)

---

## Integrating with AI Agents

Because GoKG exposes an MCP server over standard input/output (`stdio`), you can easily connect it to AI clients like Claude Desktop, Cursor, or VSCode extensions (Cline, Roo Code).

### Claude Desktop
Add this to your `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "gokg": {
      "command": "gokg",
      "args": ["mcp", "--watch"],
      "cwd": "/path/to/your/go/project"
    }
  }
}
```

### Cursor IDE
1. Open Settings -> Features -> MCP
2. Click **Add Server**
3. Set Name to `GoKG`, Type to `stdio`, and Command to `gokg mcp --watch`

### VSCode (Cline / Roo Code)
Add this to the agent's `mcp_config.json`:
```json
{
  "mcpServers": {
    "gokg": {
      "command": "gokg",
      "args": ["mcp", "--watch"]
    }
  }
}
```

---

## How AI Agents Use GoKG

A typical workflow:

```text
1. User: "The payment service crashes when calling ProcessOrder"

2. Agent calls search_nodes("ProcessOrder")
   Gets candidate fully-qualified node IDs.

3. Agent calls execute_cypher(
   "MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = \"ProcessOrder\" RETURN b.Name, b.Type LIMIT 30"
   )
   Sees exactly what ProcessOrder calls.

4. Agent calls get_blast_radius("...ProcessOrder...")
   Understands what else breaks if ProcessOrder changes.

5. Agent calls get_source_code("...ProcessOrder...")
   Reads the implementation before editing.
```

The key is that `execute_cypher` exposes the graph schema in the MCP tool description, and invalid queries fail loudly with alias/property/type errors. That gives the LLM a tight feedback loop: build a query, inspect the error or results, refine, then combine with source-code tools.

---

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

MIT License. See [LICENSE](LICENSE) for details.
