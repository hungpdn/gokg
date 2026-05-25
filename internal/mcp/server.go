package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hungpdn/gokg/internal/graph"
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
			"serverInfo":   map[string]string{"name": "gokg", "version": "0.1.0"},
		}}
	case "tools/list":
		return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "get_dependencies",
					"description": "Returns all nodes that the given node calls or imports",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"node_id": map[string]interface{}{"type": "string"},
						},
						"required": []string{"node_id"},
					},
				},
				{
					"name":        "get_blast_radius",
					"description": "Returns all nodes that depend on the given node",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"node_id": map[string]interface{}{"type": "string"},
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
							"node_id": map[string]interface{}{"type": "string"},
						},
						"required": []string{"node_id"},
					},
				},
			},
		}}
	case "tools/call":
		var params struct {
			Name      string `json:"name"`
			Arguments struct {
				NodeID string `json:"node_id"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32602, Message: "Invalid params"}}
		}

		qb := s.graph.Query()
		var nodes interface{}
		var err error

		switch params.Name {
		case "get_dependencies":
			nodes, err = qb.GetDependencies(params.Arguments.NodeID)
		case "get_blast_radius":
			nodes, err = qb.GetBlastRadius(params.Arguments.NodeID)
		case "get_concurrency_flow":
			nodes, err = qb.GetConcurrencyFlow(params.Arguments.NodeID)
		default:
			return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Method not found"}}
		}

		if err != nil {
			return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32000, Message: err.Error()}}
		}

		content, _ := json.MarshalIndent(nodes, "", "  ")

		return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(content),
				},
			},
		}}
	}

	return nil
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
