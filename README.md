# GoKG - Golang Knowledge Graph

[![Go Report Card](https://goreportcard.com/badge/github.com/hungpdn/gokg)](https://goreportcard.com/report/github.com/hungpdn/gokg)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**GoKG** is a local, open-source tool specialized in converting Golang source code into a Semantic Knowledge Graph. It acts as a "local brain" (MCP Server) providing ultra-deep architectural context for AI Coding Agents.

Unlike generic Tree-sitter-based tools, GoKG intimately understands Go semantics—including Implicit Interfaces, Goroutines, Channels, and cross-package references—making it the ultimate context engine for AI-assisted Go development.

## 🌟 Key Features

*   **Deep Semantic Parsing (Go-native)**: Extracts Packages, Files, Structs, Interfaces, Functions, and Methods.
*   **Semantic Relationships**: Accurately maps `CALLS`, `IMPORTS`, `CONTAINS`, `IMPLEMENTS` (Implicit Interface deductions), `SPAWNS` (Goroutines), `SENDS_TO`, and `RECEIVES_FROM` (Channels).
*   **Real-time Incremental Updates (Live Graph)**: Runs a background file watcher (`fsnotify`). When you hit Save, it incrementally removes and reparses only the modified package, updating the graph in milliseconds.
*   **High Performance & Local Storage**: Uses [BadgerDB](https://github.com/dgraph-io/badger) to persist the graph on your local disk. It scales to massive codebases without blowing up your RAM.
*   **MCP Server for AI Agents**: Fully compliant with the [Model Context Protocol (MCP)](https://github.com/modelcontextprotocol), providing standard JSON-RPC 2.0 `stdio` endpoints for AI models (like Claude 3.5 Sonnet, GPT-4, etc.) to query the codebase directly.
*   **Visual Export**: Export your codebase architecture to `Mermaid`, `Graphviz DOT`, or `JSON` formats.

## 🛠 Installation

Ensure you have Go 1.25+ installed.

```bash
# Clone the repository
git clone https://github.com/hungpdn/gokg.git
cd gokg

# Build and install the binary
go install ./cmd/gokg
```

## 🚀 Usage

### 1. Build the Knowledge Graph
Navigate to your Go project and run the analyzer. This will parse the AST and build a local BadgerDB database (by default in `.gokg/`).

```bash
cd /path/to/your/go/project
gokg analyze

# Rebuild the database from scratch when you want to discard stale data
gokg analyze --db .gokg/ --rebuild
```

*Options:*
*   `--module`: Explicitly set your module prefix (defaults to reading from `go.mod`).
*   `--db`: Set a custom path for the local database (default: `.gokg/`).
*   `--rebuild`: Delete the selected database directory before analysis and build a clean graph.
*   `--gc`: Run Badger value-log garbage collection after analysis (default: `true`).

GoKG uses compact BadgerDB defaults for local repositories, but Badger may still
allocate files that are larger than the live graph data because of mmap and file
preallocation.

### 2. Run the MCP Server for AI
Once the graph is built, you can start the MCP Server. AI IDEs (like Cursor, Zed) or agents can connect to this process via standard input/output.

```bash
gokg mcp
```

### 3. Export Visual Graphs
Export the semantic graph to visualize your architecture.

```bash
# Export to Mermaid Markdown
gokg export --format mermaid --out graph.md

# Export to Graphviz DOT
gokg export --format dot --out graph.dot
```

## 🧠 MCP Tools for AI Agents
When connected to an AI agent, GoKG exposes the following tools (via RAG and graph traversal algorithms):

*   `get_dependencies`: Returns all nodes that a given function calls or imports.
*   `get_blast_radius`: Returns all functions/files that depend on a given node (Reverse dependency).
*   `get_concurrency_flow`: Traces goroutines and channel data flows.
*   `get_implementations`: Finds all Structs implementing a given Interface.
*   `get_source_code`: Reads the raw Go source code of a specific node from disk.
*   `find_path`: Finds the shortest call path (BFS) between two nodes.

## 🤝 Contributing
We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) and [Code of Conduct](CODE_OF_CONDUCT.md).

## 📄 License
This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
