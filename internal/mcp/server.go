package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
			"capabilities": map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":   map[string]string{"name": "gokg", "version": "0.2.0"},
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
			"description": "Returns all nodes that the given node calls or imports, formatted as a Markdown list",
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
		b.WriteString(fmt.Sprintf("- **`%s`** (`%s`)", n.Name, n.Type))
		if n.FilePath != "" && n.Lines[0] > 0 {
			b.WriteString(fmt.Sprintf(" — `%s` L%d-%d", n.FilePath, n.Lines[0], n.Lines[1]))
		} else if n.PkgPath != "" {
			b.WriteString(fmt.Sprintf(" — pkg: `%s`", n.PkgPath))
		}
		b.WriteByte('\n')
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
