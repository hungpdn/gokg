package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hungpdn/gokg/internal/cypher"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
)

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolDefinition struct {
	name        string
	description string
	inputSchema map[string]interface{}
	handler     func(*Server, context.Context, interface{}, json.RawMessage) *Response
}

func (d toolDefinition) metadata() map[string]interface{} {
	return map[string]interface{}{
		"name":        d.name,
		"description": d.description,
		"inputSchema": d.inputSchema,
	}
}

func (s *Server) toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			name:        "get_dependencies",
			description: "Returns nodes reached by dependency edges (CALLS, IMPORTS, REFERENCES, INSTANTIATES), formatted as a Markdown list",
			inputSchema: objectSchema(map[string]interface{}{
				"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID (e.g. package path, func ID)"},
			}, "node_id"),
			handler: (*Server).handleGetDependenciesTool,
		},
		{
			name:        "get_blast_radius",
			description: "Returns all nodes that depend on the given node, formatted as a Markdown list",
			inputSchema: objectSchema(map[string]interface{}{
				"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID"},
			}, "node_id"),
			handler: (*Server).handleGetBlastRadiusTool,
		},
		{
			name:        "get_concurrency_flow",
			description: "Returns goroutines and channels connected to this node",
			inputSchema: objectSchema(map[string]interface{}{
				"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID"},
			}, "node_id"),
			handler: (*Server).handleGetConcurrencyFlowTool,
		},
		{
			name:        "get_concurrency_graph",
			description: "Returns goroutine and channel topology connected to a function, formatted as Markdown",
			inputSchema: objectSchema(map[string]interface{}{
				"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified function node ID"},
			}, "node_id"),
			handler: (*Server).handleGetConcurrencyGraphTool,
		},
		{
			name:        "get_implementations",
			description: "Returns all structs that implement a given interface",
			inputSchema: objectSchema(map[string]interface{}{
				"interface_id": map[string]interface{}{"type": "string", "description": "Fully qualified interface node ID"},
			}, "interface_id"),
			handler: (*Server).handleGetImplementationsTool,
		},
		{
			name:        "get_source_code",
			description: "Reads the actual Go source code of a function, type, or route registration node from disk",
			inputSchema: objectSchema(map[string]interface{}{
				"node_id": map[string]interface{}{"type": "string", "description": "Fully qualified node ID with line info"},
			}, "node_id"),
			handler: (*Server).handleGetSourceCodeTool,
		},
		{
			name:        "get_repository_structure",
			description: "Returns the repository folder/package/file structure from the knowledge graph, formatted as a Markdown tree",
			inputSchema: objectSchema(map[string]interface{}{
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
			}),
			handler: (*Server).handleGetRepositoryStructureTool,
		},
		{
			name:        "find_path",
			description: "Finds the shortest call path between two nodes using BFS",
			inputSchema: objectSchema(map[string]interface{}{
				"source_id": map[string]interface{}{"type": "string", "description": "Fully qualified source node ID"},
				"target_id": map[string]interface{}{"type": "string", "description": "Fully qualified target node ID"},
			}, "source_id", "target_id"),
			handler: (*Server).handleFindPathTool,
		},
		{
			name:        "search_nodes",
			description: "Searches for nodes by short name, struct name, or package name (case-insensitive) and returns their fully qualified Node IDs.",
			inputSchema: objectSchema(map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "The string to search for (e.g. 'ParseWorkspace')"},
			}, "query"),
			handler: (*Server).handleSearchNodesTool,
		},
		{
			name:        "execute_cypher",
			description: cypherToolDescription,
			inputSchema: objectSchema(map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "A GoKG Cypher query string. Must include MATCH, RETURN, and LIMIT. LIMIT comes after RETURN. See tool description for full syntax and node/edge type reference.",
				},
			}, "query"),
			handler: (*Server).handleExecuteCypherTool,
		},
		{
			name:        "get_change_impact",
			description: "Analyzes Git changes against a base ref, maps changed lines to graph nodes, and returns dependency impact.",
			inputSchema: objectSchema(map[string]interface{}{
				"base_ref": map[string]interface{}{
					"type":        "string",
					"description": "Git base ref for diff analysis. Defaults to HEAD.",
				},
				"max_depth": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum inbound dependency depth. Defaults to 1.",
					"minimum":     1,
					"maximum":     impact.MaxDepthLimit,
				},
				"max_nodes": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum impacted nodes to return. Defaults to 100.",
					"minimum":     1,
					"maximum":     impact.MaxNodesLimit,
				},
				"include_untracked": map[string]interface{}{
					"type":        "boolean",
					"description": "Include untracked Git files. Defaults to true.",
				},
			}),
			handler: (*Server).handleGetChangeImpactTool,
		},
	}
}

func objectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func parseToolArgs[T any](raw json.RawMessage) (T, error) {
	var args T
	if len(raw) == 0 {
		return args, nil
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, err
	}
	return args, nil
}

func (s *Server) handleGetDependenciesTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	var args struct {
		NodeID string `json:"node_id"`
	}
	args, err := parseToolArgs[struct {
		NodeID string `json:"node_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	nodes, err := s.graph.Query().GetDependencies(args.NodeID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatNodeListMarkdown("Dependencies", args.NodeID, nodes))
}

func (s *Server) handleGetBlastRadiusTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		NodeID string `json:"node_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	nodes, err := s.graph.Query().GetBlastRadius(args.NodeID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatNodeListMarkdown("Blast Radius", args.NodeID, nodes))
}

func (s *Server) handleGetConcurrencyFlowTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		NodeID string `json:"node_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	nodes, err := s.graph.Query().GetConcurrencyFlow(args.NodeID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatNodeListMarkdown("Concurrency Flow", args.NodeID, nodes))
}

func (s *Server) handleGetConcurrencyGraphTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		NodeID string `json:"node_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	connections, err := s.graph.Query().GetConcurrencyGraph(args.NodeID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatConcurrencyGraphMarkdown(args.NodeID, connections))
}

func (s *Server) handleGetImplementationsTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		InterfaceID string `json:"interface_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	nodes, err := s.graph.Query().GetImplementations(args.InterfaceID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatNodeListMarkdown("Implementations", args.InterfaceID, nodes))
}

func (s *Server) handleGetSourceCodeTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		NodeID string `json:"node_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	code, err := s.graph.Query().GetSourceCode(args.NodeID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatSourceCodeMarkdown(args.NodeID, code))
}

func (s *Server) handleGetRepositoryStructureTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		RepoID          string `json:"repo_id"`
		Root            string `json:"root"`
		MaxDepth        int    `json:"max_depth"`
		MaxNodes        int    `json:"max_nodes"`
		IncludePackages *bool  `json:"include_packages"`
		IncludeFiles    *bool  `json:"include_files"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	includePackages := true
	if args.IncludePackages != nil {
		includePackages = *args.IncludePackages
	}
	includeFiles := false
	if args.IncludeFiles != nil {
		includeFiles = *args.IncludeFiles
	}
	tree, err := s.graph.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		RepoID:          args.RepoID,
		Root:            args.Root,
		MaxDepth:        args.MaxDepth,
		MaxNodes:        args.MaxNodes,
		IncludePackages: includePackages,
		IncludeFiles:    includeFiles,
	})
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatRepositoryStructureMarkdown(tree))
}

func (s *Server) handleFindPathTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	pathResults, err := s.graph.Query().FindPath(args.SourceID, args.TargetID)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatPathMarkdown(args.SourceID, args.TargetID, pathResults))
}

func (s *Server) handleSearchNodesTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		Query string `json:"query"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	nodes, err := s.graph.Query().SearchNodes(args.Query)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, formatNodeListMarkdown("Search Results", args.Query, nodes))
}

func (s *Server) handleExecuteCypherTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		Query string `json:"query"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	q, err := cypher.NewParser(cypher.NewLexer(args.Query)).ParseQuery()
	if err != nil {
		return s.errorResult(id, fmt.Errorf("cypher parse error: %w", err))
	}
	if q.Limit <= 0 {
		return s.errorResult(id, errMCPCypherLimitRequired)
	}
	rows, err := s.graph.Query().ExecuteCypher(q)
	if err != nil {
		return s.errorResult(id, err)
	}
	data, err := encodeIndentedJSON(rows)
	if err != nil {
		return s.errorResult(id, fmt.Errorf("cypher result marshal error: %w", err))
	}
	return s.textResult(id, formatCypherMarkdown(args.Query, data))
}

func (s *Server) handleGetChangeImpactTool(ctx context.Context, id interface{}, raw json.RawMessage) *Response {
	args, err := parseToolArgs[struct {
		BaseRef          string `json:"base_ref"`
		MaxDepth         int    `json:"max_depth"`
		MaxNodes         int    `json:"max_nodes"`
		IncludeUntracked *bool  `json:"include_untracked"`
	}](raw)
	if err != nil {
		return s.errorResult(id, err)
	}
	if len(s.impactRepos) == 0 {
		return s.errorResult(id, fmt.Errorf("get_change_impact is unavailable because no repository roots were configured; start gokg mcp from a Go repo or workspace"))
	}
	includeUntracked := true
	if args.IncludeUntracked != nil {
		includeUntracked = *args.IncludeUntracked
	}
	report, err := impact.Analyze(ctx, s.graph, s.impactRepos, impact.Options{
		BaseRef:          args.BaseRef,
		MaxDepth:         args.MaxDepth,
		MaxNodes:         args.MaxNodes,
		IncludeUntracked: includeUntracked,
	})
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, impact.FormatMarkdown(report))
}

const cypherToolDescription = `Executes a GoKG Cypher query against the Go source code knowledge graph.

SYNTAX: MATCH <pattern> [WHERE <conditions>] RETURN <items> LIMIT <positive n>

For MCP execute_cypher calls, LIMIT is required to protect clients from unbounded result sets.

NODE TYPES (use in patterns as :TYPE):
  PACKAGE    - a Go package (e.g. github.com/org/repo/internal/parser)
  FILE       - a .go source file
  FOLDER     - a physical directory on disk
  FUNC       - a top-level function
  METHOD     - a method on a struct
  CONSTANT   - a package-scope constant
  VARIABLE   - a package-scope variable
  TYPE_ALIAS - a type alias or named non-struct/non-interface type
  STRUCT     - a struct type
  INTERFACE  - an interface type
  CHANNEL    - a channel variable (e.g. chan int)
  GOROUTINE  - a goroutine spawned with 'go'
  ROUTE      - an HTTP route registration (e.g. GET /healthz)
  BOUNDARY   - an external dependency (outside the module)
  REPO       - a repository root (multi-repo workspace)
  WORKSPACE  - a multi-repo workspace root

EDGE TYPES (use in patterns as :TYPE):
  CALLS           - function/method calls another function/method
  CONTAINS        - package contains file, file contains func/struct
  IMPORTS         - file imports a package
  REFERENCES      - symbol references a package-scope symbol or type
  INSTANTIATES    - function or variable creates a composite literal of a type
  IMPLEMENTS      - struct implements an interface
  SPAWNS          - function spawns a goroutine
  SENDS_TO        - function sends to a channel
  RECEIVES_FROM   - function receives from a channel
  REGISTERS_ROUTE - function, method, or goroutine registers an HTTP route

NODE PROPERTIES (use in WHERE and RETURN):
  Name      - short identifier (e.g. "ParseWorkspace", "Storage")
  ID        - fully-qualified unique ID (e.g. "github.com/org/repo/pkg.FuncName")
  PkgPath   - Go package import path
  FilePath  - absolute path to the source file
  Type      - the NodeType string (e.g. "FUNC")
  RepoID    - repository ID in workspace mode

EDGE PROPERTIES (use in WHERE and RETURN):
  Type      - the EdgeType string (e.g. "CALLS")
  From      - source node ID
  To        - target node ID
  RepoID    - repository ID that discovered the edge

OPERATORS in WHERE:
  =          - exact match        (n.Name = "main")
  !=         - not equal          (n.Name != "init")
  CONTAINS   - substring match    (n.PkgPath CONTAINS "internal")
  AND        - combine filters    (a.Name = "A" AND b.Type = "FUNC")

Validation is strict: unknown aliases, node/edge types, properties, and trailing tokens return errors.

PATTERN SYNTAX:
  (n:FUNC)                         - node only
  (a:FUNC)-[r:CALLS]->(b:FUNC)     - outbound edge
  (a:FUNC)<-[r:CALLS]-(b:FUNC)     - inbound edge
  (a)-[r]-(b)                      - any direction, any type

EXAMPLES:
  MATCH (n:FUNC) WHERE n.PkgPath CONTAINS "parser" RETURN n LIMIT 20
  MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) WHERE a.Name = "Analyze" RETURN b.Name LIMIT 10
  MATCH (a:FUNC)-[r:CALLS]->(b) WHERE a.Name = "Analyze" AND b.Type != "BOUNDARY" RETURN b.Name, b.Type LIMIT 20
  MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE) RETURN s.Name, i.Name LIMIT 20
  MATCH (f:FUNC)-[r:SPAWNS]->(g:GOROUTINE) RETURN f.Name, g.Name LIMIT 20
  MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL) WHERE f.PkgPath CONTAINS "worker" RETURN f.Name, c.Name LIMIT 20
  MATCH (owner)-[r:REGISTERS_ROUTE]->(route:ROUTE) RETURN owner.Name, route.Name LIMIT 50
  MATCH (route:ROUTE)-[r:REFERENCES]->(handler) RETURN route.Name, handler.Name LIMIT 50
  MATCH (n:INTERFACE) WHERE n.Name CONTAINS "Storage" RETURN n LIMIT 20

Always include MATCH, RETURN, and a positive LIMIT after RETURN.`
