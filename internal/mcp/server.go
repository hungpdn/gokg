package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hungpdn/gokg/internal/cypher"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
)

type Server struct {
	graph *graph.Graph
}

func NewServer(g *graph.Graph) *Server {
	return &Server{graph: g}
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Start(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error")
			continue
		}

		res := s.handleRequest(&req)
		if res != nil {
			s.sendResponse(res)
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]string{"name": "gokg", "version": "0.2.0"},
		}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	}

	return nil
}

func (s *Server) handleToolsList(req *Request) *Response {
	tools := []map[string]interface{}{
		{
			"name":        "get_dependencies",
			"description": "Returns nodes reached by dependency edges (CALLS, IMPORTS, REFERENCES, INSTANTIATES), formatted as a Markdown list",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID (e.g. package path, func ID)"},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "get_blast_radius",
			"description": "Returns all nodes that depend on the given node, formatted as a Markdown list",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID"},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "get_concurrency_flow",
			"description": "Returns goroutines and channels connected to this node",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID"},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "get_concurrency_graph",
			"description": "Returns goroutine and channel topology connected to a function, formatted as Markdown",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified function node ID"},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "get_implementations",
			"description": "Returns all structs that implement a given interface",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"interface_id": map[string]interface{}{"type": "string", "description": "Fully qualified interface node ID"},
				},
				"required": []string{"interface_id"},
			},
		},
		{
			"name":        "get_source_code",
			"description": "Reads the actual Go source code of a function, struct, or interface node from disk",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID with line info"},
				},
				"required": []string{"node_id"},
			},
		},
		{
			"name":        "find_path",
			"description": "Finds the shortest call path between two nodes using BFS",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source_id": map[string]interface{}{"type": "string", "description": "Fully qualified source node ID"},
					"target_id": map[string]interface{}{"type": "string", "description": "Fully qualified target node ID"},
				},
				"required": []string{"source_id", "target_id"},
			},
		},
		{
			"name":        "search_nodes",
			"description": "Searches for nodes by short name, struct name, or package name (case-insensitive) and returns their fully qualified Node IDs.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "The string to search for (e.g. 'ParseWorkspace')"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name": "execute_cypher",
			"description": `Executes a GoKG Cypher query against the Go source code knowledge graph.

SYNTAX: MATCH <pattern> [WHERE <conditions>] RETURN <items> [LIMIT <n>]

NODE TYPES (use in patterns as :TYPE):
  PACKAGE    – a Go package (e.g. github.com/org/repo/internal/parser)
  FILE       – a .go source file
  FOLDER     – a physical directory on disk
  FUNC       – a top-level function
  METHOD     – a method on a struct
  VAR        – a package or local variable node
  STRUCT     – a struct type
  INTERFACE  – an interface type
  CHANNEL    – a channel variable (e.g. chan int)
  GOROUTINE  – a goroutine spawned with 'go'
  BOUNDARY   – an external dependency (outside the module)
  REPO       – a repository root (multi-repo workspace)
  WORKSPACE  – a multi-repo workspace root

EDGE TYPES (use in patterns as :TYPE):
  CALLS          – function/method calls another function/method
  CONTAINS       – package contains file, file contains func/struct
  IMPORTS        – file imports a package
  REFERENCES     – symbol references a package-scope symbol or type
  INSTANTIATES   – function or variable creates a composite literal of a type
  IMPLEMENTS     – struct implements an interface
  SPAWNS         – function spawns a goroutine
  SENDS_TO       – function sends to a channel
  RECEIVES_FROM  – function receives from a channel

NODE PROPERTIES (use in WHERE and RETURN):
  Name      – short identifier (e.g. "ParseWorkspace", "Storage")
  ID        – fully-qualified unique ID (e.g. "github.com/org/repo/pkg.FuncName")
  PkgPath   – Go package import path
  FilePath  – absolute path to the source file
  Type      – the NodeType string (e.g. "FUNC")
  RepoID    – repository ID in workspace mode

EDGE PROPERTIES (use in WHERE and RETURN):
  Type      – the EdgeType string (e.g. "CALLS")
  From      – source node ID
  To        – target node ID
  RepoID    – repository ID that discovered the edge

OPERATORS in WHERE:
  =          – exact match        (n.Name = "main")
  !=         – not equal          (n.Name != "init")
  CONTAINS   – substring match    (n.PkgPath CONTAINS "internal")
  AND        – combine filters    (a.Name = "A" AND b.Type = "FUNC")

Validation is strict: unknown aliases, node/edge types, properties, and trailing tokens return errors.

PATTERN SYNTAX:
  (n:FUNC)                         – node only
  (a:FUNC)-[r:CALLS]->(b:FUNC)     – outbound edge
  (a:FUNC)<-[r:CALLS]-(b:FUNC)     – inbound edge
  (a)-[r]-(b)                      – any direction, any type

EXAMPLES:
  MATCH (n:FUNC) WHERE n.PkgPath CONTAINS "parser" RETURN n LIMIT 20
  MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) WHERE a.Name = "Analyze" RETURN b.Name LIMIT 10
  MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = "Analyze" AND b.Type != "BOUNDARY" RETURN b.Name, b.Type LIMIT 20
  MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) RETURN s.Name, i.Name
  MATCH (f:FUNC)-[r:SPAWNS]->(g:GOROUTINE) RETURN f.Name, g.Name
  MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) WHERE f.PkgPath CONTAINS "worker" RETURN f.Name, c.Name
  MATCH (n:INTERFACE) WHERE n.Name CONTAINS "Storage" RETURN n

Always include MATCH and RETURN. Use LIMIT after RETURN to cap large results.`,
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "A GoKG Cypher query string. Must include MATCH and RETURN; optional LIMIT comes after RETURN. See tool description for full syntax and node/edge type reference.",
					},
				},
				"required": []string{"query"},
			},
		},
	}

	return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
		"tools": tools,
	}}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			NodeID      string `json:"node_id"`
			InterfaceID string `json:"interface_id"`
			SourceID    string `json:"source_id"`
			TargetID    string `json:"target_id"`
			Query       string `json:"query"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32602, Message: "Invalid params"}}
	}

	qb := s.graph.Query()

	switch params.Name {
	case "get_dependencies":
		nodes, err := qb.GetDependencies(params.Arguments.NodeID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatNodeListMarkdown("Dependencies", params.Arguments.NodeID, nodes))

	case "get_blast_radius":
		nodes, err := qb.GetBlastRadius(params.Arguments.NodeID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatNodeListMarkdown("Blast Radius", params.Arguments.NodeID, nodes))

	case "get_concurrency_flow":
		nodes, err := qb.GetConcurrencyFlow(params.Arguments.NodeID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatNodeListMarkdown("Concurrency Flow", params.Arguments.NodeID, nodes))

	case "get_concurrency_graph":
		connections, err := qb.GetConcurrencyGraph(params.Arguments.NodeID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatConcurrencyGraphMarkdown(params.Arguments.NodeID, connections))

	case "get_implementations":
		nodes, err := qb.GetImplementations(params.Arguments.InterfaceID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatNodeListMarkdown("Implementations", params.Arguments.InterfaceID, nodes))

	case "get_source_code":
		code, err := qb.GetSourceCode(params.Arguments.NodeID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatSourceCodeMarkdown(params.Arguments.NodeID, code))

	case "find_path":
		pathResults, err := qb.FindPath(params.Arguments.SourceID, params.Arguments.TargetID)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatPathMarkdown(params.Arguments.SourceID, params.Arguments.TargetID, pathResults))

	case "search_nodes":
		nodes, err := qb.SearchNodes(params.Arguments.Query)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatNodeListMarkdown("Search Results", params.Arguments.Query, nodes))

	case "execute_cypher":
		q, err := cypher.NewParser(cypher.NewLexer(params.Arguments.Query)).ParseQuery()
		if err != nil {
			return s.errorResult(req.ID, fmt.Errorf("cypher parse error: %w", err))
		}
		rows, err := qb.ExecuteCypher(q)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			return s.errorResult(req.ID, fmt.Errorf("cypher result marshal error: %w", err))
		}
		return s.textResult(req.ID, formatCypherMarkdown(params.Arguments.Query, string(data)))

	default:
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Unknown tool: " + params.Name}}
	}
}

// --- Markdown formatting helpers ---

func formatNodeListMarkdown(title, nodeID string, nodes []*parser.Node) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## %s of `%s`\n\n", title, nodeID))

	if len(nodes) == 0 {
		b.WriteString("_No results found._\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("Found **%d** node(s):\n\n", len(nodes)))
	for _, n := range nodes {
		b.WriteString(fmt.Sprintf("- **`%s`** (`%s`) — ID: `%s`", n.Name, n.Type, n.ID))
		if n.FilePath != "" && n.Lines[0] > 0 {
			b.WriteString(fmt.Sprintf(" — `%s` L%d-%d", n.FilePath, n.Lines[0], n.Lines[1]))
		} else if n.PkgPath != "" {
			b.WriteString(fmt.Sprintf(" — pkg: `%s`", n.PkgPath))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatConcurrencyGraphMarkdown(nodeID string, connections []graph.ConcurrencyConnection) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Concurrency Graph of `%s`\n\n", nodeID))

	if len(connections) == 0 {
		b.WriteString("_No concurrency nodes found._\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("Found **%d** connection(s):\n\n", len(connections)))
	for _, conn := range connections {
		if conn.Node == nil || conn.Edge == nil {
			continue
		}

		if conn.Direction == "inbound" {
			b.WriteString(fmt.Sprintf("- **`%s`** (`%s`) --_%s_--> `%s`\n", conn.Node.Name, conn.Node.Type, conn.Edge.Type, nodeID))
		} else {
			b.WriteString(fmt.Sprintf("- `%s` --_%s_--> **`%s`** (`%s`)\n", nodeID, conn.Edge.Type, conn.Node.Name, conn.Node.Type))
		}
	}

	return b.String()
}

func formatSourceCodeMarkdown(nodeID, code string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Source Code of `%s`\n\n", nodeID))
	b.WriteString("```go\n")
	b.WriteString(code)
	b.WriteString("```\n")
	return b.String()
}

func formatPathMarkdown(sourceID, targetID string, pathResults []graph.PathResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Shortest Path: `%s` → `%s`\n\n", sourceID, targetID))

	if len(pathResults) == 0 {
		b.WriteString("_No path found._\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("Path length: **%d** hop(s)\n\n", len(pathResults)-1))
	for i, pr := range pathResults {
		prefix := "  "
		if i == 0 {
			prefix = "▶ "
		} else if i == len(pathResults)-1 {
			prefix = "◉ "
		} else {
			prefix = "→ "
		}

		b.WriteString(fmt.Sprintf("%s**`%s`** (`%s`)\n", prefix, pr.Node.Name, pr.Node.Type))
		if pr.EdgeType != "" {
			b.WriteString(fmt.Sprintf("  ↓ _%s_\n", pr.EdgeType))
		}
	}
	return b.String()
}

func formatCypherMarkdown(query, jsonData string) string {
	var b strings.Builder
	b.WriteString("## Cypher Query Results\n\n")
	b.WriteString(fmt.Sprintf("**Query:**\n```cypher\n%s\n```\n\n", query))
	b.WriteString("**Results:**\n```json\n")
	b.WriteString(jsonData)
	b.WriteString("\n```\n")
	return b.String()
}

// --- Response helpers ---

func (s *Server) textResult(id interface{}, text string) *Response {
	return &Response{ID: id, JSONRPC: "2.0", Result: map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
	}}
}

func (s *Server) errorResult(id interface{}, err error) *Response {
	return &Response{ID: id, JSONRPC: "2.0", Error: &Error{Code: -32000, Message: err.Error()}}
}

func (s *Server) sendResponse(res *Response) {
	data, _ := json.Marshal(res)
	fmt.Printf("%s\n", data)
}

func (s *Server) sendError(id interface{}, code int, message string) {
	res := &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	}
	s.sendResponse(res)
}
