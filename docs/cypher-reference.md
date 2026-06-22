# GoKG Cypher Query Language Reference

GoKG supports a strict, read-only subset of Cypher for querying the in-memory Go knowledge graph. You can use it through the CLI (`gokg query`) or through the MCP `execute_cypher` tool.

---

## Syntax

```cypher
MATCH <pattern> [WHERE <conditions>] RETURN <items> [LIMIT <positive n>]
```

- `MATCH` is required and describes the graph pattern.
- `WHERE` is optional and filters matched aliases by properties.
- `RETURN` is required and selects aliases or alias properties.
- `LIMIT` is optional for CLI queries, must be a positive integer when present, and must come after `RETURN`.
- MCP `execute_cypher` calls require `LIMIT` to protect clients from unbounded result sets.
- A line whose first non-whitespace characters are `--` is treated as a comment.

Keywords, node types, edge types, operators, and property names are matched case-insensitively. Result keys preserve the property casing used in `RETURN`.

Validation is strict: unknown aliases, node/edge types, properties, unsupported operators, and trailing tokens return errors instead of silently widening the result set.

---

## Node Types

Use node types after `:` inside parentheses: `(alias:TYPE)`.

| Type | Description | Example ID |
|---|---|---|
| `PACKAGE` | Go package | `github.com/org/repo/internal/parser` |
| `FILE` | Go source file | `/home/user/repo/main.go` |
| `FOLDER` | Physical directory | `/home/user/repo/internal` |
| `FUNC` | Top-level function | `github.com/org/repo/parser.ParseWorkspace` |
| `METHOD` | Method on a type | `github.com/org/repo/*Graph.AddNode` |
| `CONSTANT` | Package-scope constant | `github.com/org/repo/pkg.DefaultLimit` |
| `VARIABLE` | Package-scope variable | `github.com/org/repo/pkg.cache` |
| `TYPE_ALIAS` | Type alias or non-struct/non-interface named type | `github.com/org/repo/pkg.NodeID` |
| `STRUCT` | Struct type | `github.com/org/repo/graph.Graph` |
| `INTERFACE` | Interface type | `github.com/org/repo/storage.Storage` |
| `CHANNEL` | Channel variable | `github.com/org/repo/pkg.funcName.chanName` |
| `GOROUTINE` | Goroutine spawned with `go` | `github.com/org/repo/pkg.funcName.goroutine_L42` |
| `BOUNDARY` | External dependency | `fmt.Println` |
| `REPO` | Repository root in workspace mode | `github.com/org/service-a` |
| `WORKSPACE` | Multi-repo workspace root | `my-platform` |

---

## Edge Types

Use edge types after `:` inside square brackets: `[alias:TYPE]`.

| Type | Direction | Meaning |
|---|---|---|
| `CALLS` | `(a)-[:CALLS]->(b)` | Function or method `a` calls `b` |
| `CONTAINS` | `(a)-[:CONTAINS]->(b)` | Package/file/folder contains another node |
| `IMPORTS` | `(a)-[:IMPORTS]->(b)` | File `a` imports package `b` |
| `REFERENCES` | `(a)-[:REFERENCES]->(b)` | Symbol `a` references package-scope symbol or type `b` |
| `INSTANTIATES` | `(a)-[:INSTANTIATES]->(b)` | Function or variable `a` creates a composite literal of type `b` |
| `IMPLEMENTS` | `(a)-[:IMPLEMENTS]->(b)` | Struct `a` implements interface `b` |
| `SPAWNS` | `(a)-[:SPAWNS]->(b)` | Function `a` spawns goroutine `b` |
| `SENDS_TO` | `(a)-[:SENDS_TO]->(b)` | Function `a` sends to channel `b` |
| `RECEIVES_FROM` | `(a)-[:RECEIVES_FROM]->(b)` | Function `a` receives from channel `b` |

---

## Properties

Use properties after `.` in `WHERE` clauses and `RETURN` items.

Node properties:

| Property | Type | Description |
|---|---|---|
| `Name` | string | Short identifier |
| `ID` | string | Fully-qualified unique ID |
| `PkgPath` | string | Go package import path |
| `FilePath` | string | Absolute source file path |
| `Type` | string | Node type string |
| `RepoID` | string | Repository ID in workspace mode |

Edge properties:

| Property | Type | Description |
|---|---|---|
| `Type` | string | Edge type string |
| `From` | string | Source node ID |
| `To` | string | Target node ID |
| `RepoID` | string | Repository ID that discovered the edge |

---

## WHERE Operators

| Operator | Example | Meaning |
|---|---|---|
| `=` | `n.Name = "main"` | Exact string match |
| `!=` | `n.Type != "BOUNDARY"` | Not equal |
| `CONTAINS` | `n.PkgPath CONTAINS "internal"` | Substring match |
| `AND` | `a.Name = "A" AND b.Type = "FUNC"` | Combine conditions |

String values may use single or double quotes. Multiple adjacent conditions are also treated as `AND`, but explicit `AND` is clearer for generated queries.

---

## Pattern Syntax

### Node-only Pattern

```cypher
MATCH (n:FUNC) RETURN n
```

### Outbound Edge

```cypher
MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) RETURN a.Name, b.Name
```

### Inbound Edge

```cypher
MATCH (target:FUNC)<-[r:CALLS]-(caller) RETURN caller.Name, caller.Type
```

### Any Direction

```cypher
MATCH (a)-[r]-(b) WHERE a.Name = "Analyze" RETURN b.Name, r.Type
```

### Anonymous Aliases

Aliases can be omitted only when you do not reference that node or edge in `WHERE` or `RETURN`.

```cypher
MATCH (:PACKAGE)-[:CONTAINS]->(f:FILE) RETURN f.Name, f.FilePath LIMIT 20
```

---

## Examples by Use Case

### Explore Nodes

```cypher
-- All functions in a package
MATCH (n:FUNC) WHERE n.PkgPath CONTAINS "parser" RETURN n.Name, n.ID LIMIT 20

-- All interfaces in the codebase
MATCH (n:INTERFACE) RETURN n.Name, n.PkgPath LIMIT 30

-- Find a specific struct by name
MATCH (n:STRUCT) WHERE n.Name = "Graph" RETURN n

-- Files in packages whose name contains storage
MATCH (pkg:PACKAGE)-[r:CONTAINS]->(f:FILE) WHERE pkg.Name CONTAINS "storage" RETURN f.Name, f.FilePath
```

### Call Graph

```cypher
-- Everything a function calls
MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = "BuildFromParseResult" RETURN b.Name, b.Type LIMIT 30

-- Exclude external boundary calls
MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = "Analyze" AND b.Type != "BOUNDARY" RETURN b.Name, b.ID LIMIT 30

-- Who calls a specific function
MATCH (caller)-[r:CALLS]->(target:FUNC) WHERE target.Name = "AddEdge" RETURN caller.Name, caller.Type, caller.ID

-- Inspect edge properties
MATCH (a:FUNC)-[r]->(b) WHERE r.Type = "CALLS" RETURN r.From, r.To, r.Type LIMIT 20
```

### Interface Implementations

```cypher
-- All structs implementing an interface
MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) WHERE i.Name = "Storage" RETURN s.Name, s.PkgPath

-- All interface/struct pairs in the module
MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) RETURN s.Name, i.Name LIMIT 30
```

### Concurrency Analysis

```cypher
-- All goroutines in the codebase
MATCH (g:GOROUTINE) RETURN g.Name, g.FilePath LIMIT 20

-- Functions that spawn goroutines
MATCH (f:FUNC)-[r:SPAWNS]->(g:GOROUTINE) RETURN f.Name, g.Name

-- Channel send/receive flows
MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) RETURN f.Name, c.Name
MATCH (f:FUNC)-[r:RECEIVES_FROM]->(c:CHANNEL) RETURN f.Name, c.Name

-- Full concurrency topology around a function
MATCH (a)-[r]-(b) WHERE a.Name = "worker" RETURN a.Name, r.Type, b.Name, b.Type
```

### Package Structure

```cypher
-- All packages in the module
MATCH (pkg:PACKAGE) WHERE pkg.PkgPath CONTAINS "gokg" RETURN pkg.Name, pkg.PkgPath

-- What a file imports
MATCH (f:FILE)-[r:IMPORTS]->(pkg) WHERE f.Name CONTAINS "analyze" RETURN pkg.ID

-- Physical folder tree
MATCH (folder:FOLDER)-[r:CONTAINS]->(child) RETURN folder.Name, child.Name, child.Type LIMIT 40
```

### Multi-Repo Workspaces

```cypher
-- All repositories in the workspace
MATCH (r:REPO) RETURN r.Name, r.ID

-- All functions in a specific repo
MATCH (n:FUNC) WHERE n.RepoID = "github.com/org/service-a" RETURN n.Name, n.PkgPath LIMIT 20

-- Cross-repo: who in repo A calls something in repo B
MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) WHERE a.RepoID = "github.com/org/service-a" AND b.RepoID = "github.com/org/service-b" RETURN a.Name, b.Name, b.ID LIMIT 20
```

---

## Usage via CLI

```bash
# Analyze your project first
gokg analyze

# Run against the default .gokg/ database
gokg query 'MATCH (n:FUNC) WHERE n.Name = "main" RETURN n'

# Run against a custom database
gokg query --db /path/to/.gokg 'MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) RETURN a.Name, b.Name LIMIT 10'

# Run against a merged workspace graph
gokg query --workspace my-platform 'MATCH (n:FUNC) RETURN n.Name, n.RepoID LIMIT 20'
```

---

## Usage via MCP / AI Agent

When `gokg mcp` is running, a connected AI agent can call the `execute_cypher` tool:

```json
{
  "method": "tools/call",
  "params": {
    "name": "execute_cypher",
    "arguments": {
      "query": "MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) RETURN s.Name, i.Name LIMIT 20"
    }
  }
}
```

The response is Markdown containing the original query and a JSON array of result rows.

---

## Tips for AI Agents

1. Always include `MATCH` and `RETURN`.
2. Prefer explicit aliases for every node and edge you need to filter or return.
3. Use `CONTAINS` for fuzzy discovery, then tighten with `=` once you know the exact ID.
4. Use a positive `LIMIT` on exploratory queries.
5. Use `search_nodes` first when you do not know the exact node name or ID.
6. Use `find_path` for shortest paths; this Cypher subset is single-hop only.
7. Treat validation errors as feedback and regenerate the query with the listed aliases/properties/types.

Example workflow:

```text
1. User: "Find all places that use the Storage interface"
2. Agent calls search_nodes("Storage")
3. Agent calls execute_cypher(
   "MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) WHERE i.Name = \"Storage\" RETURN s.Name, s.PkgPath LIMIT 20"
   )
4. Agent calls get_source_code for the relevant interface or implementors.
5. Agent synthesizes the answer with exact node IDs and source references.
```

---

## Limitations

| Feature | Support |
|---|---|
| `MATCH` single-hop patterns | Yes |
| `WHERE` with `=`, `!=`, `CONTAINS`, `AND` | Yes |
| `RETURN` aliases and properties | Yes |
| `LIMIT` with positive integers | Yes |
| Line comments with `--` | Yes |
| Multi-hop patterns | No, use `find_path` |
| `OR` conditions | No |
| Aggregation (`COUNT`, `SUM`) | No |
| `ORDER BY` | No |
| Mutations (`CREATE`, `DELETE`, `SET`) | No |
