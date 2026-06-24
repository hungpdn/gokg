package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hungpdn/gokg/internal/cypher"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/version"
)

type Server struct {
	graph *graph.Graph
}

const (
	maxStdioMessageBytes = 4 << 20

	latestMCPProtocolVersion = "2025-06-18"
	legacyMCPProtocolVersion = "2024-11-05"
)

var errMCPCypherLimitRequired = errors.New("execute_cypher requires LIMIT to protect MCP clients from unbounded result sets")

var supportedMCPProtocolVersions = map[string]struct{}{
	latestMCPProtocolVersion: {},
	legacyMCPProtocolVersion: {},
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
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStdioMessageBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}

		line := scanner.Bytes()

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := writeError(out, nil, -32700, "Parse error"); err != nil {
				return err
			}
			continue
		}

		res := s.handleRequest(&req)
		if res != nil {
			if err := writeResponse(out, res); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequest(req *Request) *Response {
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32600, Message: "Invalid Request"}}
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	}

	if req.ID != nil {
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Method not found: " + req.Method}}
	}
	return nil
}

func (s *Server) handleInitialize(req *Request) *Response {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32602, Message: "Invalid params"}}
		}
	}

	return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
		"protocolVersion": negotiateMCPProtocolVersion(params.ProtocolVersion),
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"serverInfo":      map[string]string{"name": "gokg", "version": version.Get().Version},
	}}
}

func negotiateMCPProtocolVersion(requested string) string {
	if _, ok := supportedMCPProtocolVersions[requested]; ok {
		return requested
	}
	return latestMCPProtocolVersion
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
			"name":        "get_repository_structure",
			"description": "Returns the repository folder/package/file structure from the knowledge graph, formatted as a Markdown tree",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo_id": map[string]interface{}{"type": "string", "description": "Repository ID in workspace mode. Optional for single-repo graphs."},
					"root":    map[string]interface{}{"type": "string", "description": "Folder node ID or repository-relative path. Defaults to the repository root."},
					"max_depth": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum tree depth to return. Defaults to %d.", graph.RepositoryStructureDefaultMaxDepth),
						"minimum":     1,
						"maximum":     graph.RepositoryStructureMaxDepth,
					},
					"max_nodes": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Maximum number of tree nodes to return. Defaults to %d.", graph.RepositoryStructureDefaultMaxNodes),
						"minimum":     1,
						"maximum":     graph.RepositoryStructureMaxNodes,
					},
					"include_packages": map[string]interface{}{"type": "boolean", "description": "Include package nodes. Defaults to true."},
					"include_files":    map[string]interface{}{"type": "boolean", "description": "Include file nodes below package nodes. Defaults to false."},
				},
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

SYNTAX: MATCH <pattern> [WHERE <conditions>] RETURN <items> LIMIT <positive n>

For MCP execute_cypher calls, LIMIT is required to protect clients from unbounded result sets.

NODE TYPES (use in patterns as :TYPE):
  PACKAGE    – a Go package (e.g. github.com/org/repo/internal/parser)
  FILE       – a .go source file
  FOLDER     – a physical directory on disk
  FUNC       – a top-level function
  METHOD     – a method on a struct
  CONSTANT   – a package-scope constant
  VARIABLE   – a package-scope variable
  TYPE_ALIAS – a type alias or named non-struct/non-interface type
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
  MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) RETURN s.Name, i.Name LIMIT 20
  MATCH (f:FUNC)-[r:SPAWNS]->(g:GOROUTINE) RETURN f.Name, g.Name LIMIT 20
  MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) WHERE f.PkgPath CONTAINS "worker" RETURN f.Name, c.Name LIMIT 20
  MATCH (n:INTERFACE) WHERE n.Name CONTAINS "Storage" RETURN n LIMIT 20

Always include MATCH, RETURN, and a positive LIMIT after RETURN.`,
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "A GoKG Cypher query string. Must include MATCH, RETURN, and LIMIT. LIMIT comes after RETURN. See tool description for full syntax and node/edge type reference.",
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
			RepoID      string `json:"repo_id"`
			Root        string `json:"root"`
			MaxDepth    int    `json:"max_depth"`
			MaxNodes    int    `json:"max_nodes"`

			IncludePackages *bool `json:"include_packages"`
			IncludeFiles    *bool `json:"include_files"`
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

	case "get_repository_structure":
		includePackages := true
		if params.Arguments.IncludePackages != nil {
			includePackages = *params.Arguments.IncludePackages
		}
		includeFiles := false
		if params.Arguments.IncludeFiles != nil {
			includeFiles = *params.Arguments.IncludeFiles
		}
		tree, err := qb.GetRepositoryStructure(graph.RepositoryStructureOptions{
			RepoID:          params.Arguments.RepoID,
			Root:            params.Arguments.Root,
			MaxDepth:        params.Arguments.MaxDepth,
			MaxNodes:        params.Arguments.MaxNodes,
			IncludePackages: includePackages,
			IncludeFiles:    includeFiles,
		})
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		return s.textResult(req.ID, formatRepositoryStructureMarkdown(tree))

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
		if q.Limit <= 0 {
			return s.errorResult(req.ID, errMCPCypherLimitRequired)
		}
		rows, err := qb.ExecuteCypher(q)
		if err != nil {
			return s.errorResult(req.ID, err)
		}
		data, err := encodeIndentedJSON(rows)
		if err != nil {
			return s.errorResult(req.ID, fmt.Errorf("cypher result marshal error: %w", err))
		}
		return s.textResult(req.ID, formatCypherMarkdown(params.Arguments.Query, data))

	default:
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Unknown tool: " + params.Name}}
	}
}

// --- Markdown formatting helpers ---

func formatNodeListMarkdown(title, nodeID string, nodes []*parser.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s of `%s`\n\n", title, nodeID)

	if len(nodes) == 0 {
		b.WriteString("_No results found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Found **%d** node(s):\n\n", len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&b, "- **`%s`** (`%s`) — ID: `%s`", n.Name, n.Type, n.ID)
		if n.FilePath != "" && n.Lines[0] > 0 && n.Lines[1] >= n.Lines[0] {
			fmt.Fprintf(&b, " — `%s` L%d-%d", n.FilePath, n.Lines[0], n.Lines[1])
		} else if n.PkgPath != "" {
			fmt.Fprintf(&b, " — pkg: `%s`", n.PkgPath)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatConcurrencyGraphMarkdown(nodeID string, connections []graph.ConcurrencyConnection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Concurrency Graph of `%s`\n\n", nodeID)

	if len(connections) == 0 {
		b.WriteString("_No concurrency nodes found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Found **%d** connection(s):\n\n", len(connections))
	for _, conn := range connections {
		if conn.Node == nil || conn.Edge == nil {
			continue
		}

		if conn.Direction == "inbound" {
			fmt.Fprintf(&b, "- **`%s`** (`%s`) --_%s_--> `%s`\n", conn.Node.Name, conn.Node.Type, conn.Edge.Type, nodeID)
		} else {
			fmt.Fprintf(&b, "- `%s` --_%s_--> **`%s`** (`%s`)\n", nodeID, conn.Edge.Type, conn.Node.Name, conn.Node.Type)
		}
	}

	return b.String()
}

func formatSourceCodeMarkdown(nodeID, code string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Source Code of `%s`\n\n", nodeID)
	writeMarkdownFencedBlock(&b, "go", code)
	return b.String()
}

func formatRepositoryStructureMarkdown(root *graph.RepositoryStructureNode) string {
	var b strings.Builder
	b.WriteString("## Repository Structure\n\n")
	if root == nil || root.Node == nil {
		b.WriteString("_No repository structure found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "%s\n", repositoryStructureLabel(root.Node))
	for i, child := range root.Children {
		writeRepositoryStructureNode(&b, child, "", i == len(root.Children)-1)
	}
	return b.String()
}

func writeRepositoryStructureNode(b *strings.Builder, node *graph.RepositoryStructureNode, prefix string, last bool) {
	if node == nil || node.Node == nil {
		return
	}
	connector := "|-- "
	nextPrefix := prefix + "|   "
	if last {
		connector = "└─- "
		nextPrefix = prefix + "    "
	}
	fmt.Fprintf(b, "%s%s%s\n", prefix, connector, repositoryStructureLabel(node.Node))
	for i, child := range node.Children {
		writeRepositoryStructureNode(b, child, nextPrefix, i == len(node.Children)-1)
	}
}

func repositoryStructureLabel(node *parser.Node) string {
	name := markdownInlineCode(node.Name)
	switch node.Type {
	case parser.NodeTypeFolder:
		return fmt.Sprintf("%s (`%s`)", markdownInlineCode(node.Name+"/"), node.Type)
	case parser.NodeTypePackage:
		return fmt.Sprintf("%s (`%s`, pkg: %s)", name, node.Type, markdownInlineCode(node.ID))
	case parser.NodeTypeFile:
		return fmt.Sprintf("%s (`%s`)", name, node.Type)
	default:
		return fmt.Sprintf("%s (`%s`)", name, node.Type)
	}
}

func formatPathMarkdown(sourceID, targetID string, pathResults []graph.PathResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Shortest Path: `%s` → `%s`\n\n", sourceID, targetID)

	if len(pathResults) == 0 {
		b.WriteString("_No path found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Path length: **%d** hop(s)\n\n", len(pathResults)-1)
	for i, pr := range pathResults {
		var prefix string
		if i == 0 {
			prefix = "▶ "
		} else if i == len(pathResults)-1 {
			prefix = "◉ "
		} else {
			prefix = "→ "
		}

		fmt.Fprintf(&b, "%s**`%s`** (`%s`)\n", prefix, pr.Node.Name, pr.Node.Type)
		if pr.EdgeType != "" {
			fmt.Fprintf(&b, "  ↓ _%s_\n", pr.EdgeType)
		}
	}
	return b.String()
}

func formatCypherMarkdown(query, jsonData string) string {
	var b strings.Builder
	b.WriteString("## Cypher Query Results\n\n")
	b.WriteString("**Query:**\n")
	writeMarkdownFencedBlock(&b, "cypher", query)
	b.WriteByte('\n')
	b.WriteString("**Results:**\n")
	writeMarkdownFencedBlock(&b, "json", jsonData)
	return b.String()
}

func writeMarkdownFencedBlock(b *strings.Builder, language string, content string) {
	fence := markdownFence(content)
	b.WriteString(fence)
	b.WriteString(language)
	b.WriteByte('\n')
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(fence)
	b.WriteByte('\n')
}

func markdownFence(content string) string {
	maxRun := maxBacktickRun(content)
	if maxRun < 3 {
		maxRun = 3
	} else {
		maxRun++
	}
	return strings.Repeat("`", maxRun)
}

func markdownInlineCode(value string) string {
	value = strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	fence := strings.Repeat("`", maxBacktickRun(value)+1)
	if value == "" {
		return fence + " " + fence
	}
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") ||
		strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return fence + " " + value + " " + fence
	}
	return fence + value + fence
}

func maxBacktickRun(value string) int {
	maxRun := 0
	currentRun := 0
	for _, r := range value {
		if r == '`' {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
			continue
		}
		currentRun = 0
	}
	return maxRun
}

func encodeIndentedJSON(value interface{}) (string, error) {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
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

func writeError(out io.Writer, id interface{}, code int, message string) error {
	return writeResponse(out, &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	})
}

func writeResponse(out io.Writer, res *Response) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}
