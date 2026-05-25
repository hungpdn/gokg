package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleInitialize(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}

	res := server.handleRequest(req)
	assert.NotNil(t, res)
	assert.Equal(t, "2.0", res.JSONRPC)
	assert.Equal(t, 1, res.ID)

	resultMap, ok := res.Result.(map[string]interface{})
	assert.True(t, ok)
	serverInfo, ok := resultMap["serverInfo"].(map[string]string)
	assert.True(t, ok)
	assert.Equal(t, "gokg", serverInfo["name"])
}

func TestHandleListTools(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	req := &Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	res := server.handleRequest(req)
	assert.NotNil(t, res)
	assert.Equal(t, "2.0", res.JSONRPC)

	resultMap, ok := res.Result.(map[string]interface{})
	assert.True(t, ok)
	tools, ok := resultMap["tools"].([]map[string]interface{})
	assert.True(t, ok)
	assert.Len(t, tools, 6, "Should have 6 tools registered")

	// Verify new tools are present
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool["name"].(string)] = true
	}
	assert.True(t, toolNames["get_implementations"])
	assert.True(t, toolNames["get_source_code"])
	assert.True(t, toolNames["find_path"])
}

func TestHandleCallToolError(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	// Since graph is empty, calling get_dependencies for "unknown" should return an error
	paramsRaw := []byte(`{"name": "get_dependencies", "arguments": {"node_id": "unknown_node"}}`)
	req := &Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  json.RawMessage(paramsRaw),
	}

	res := server.handleRequest(req)
	assert.NotNil(t, res)
	assert.NotNil(t, res.Error)
	assert.Contains(t, res.Error.Message, "node not found")
}

func TestHandleCallDependenciesMarkdown(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()

	n1 := &parser.Node{ID: "pkg.A", Type: parser.NodeTypeFunc, Name: "FuncA", PkgPath: "pkg"}
	n2 := &parser.Node{ID: "pkg.B", Type: parser.NodeTypeFunc, Name: "FuncB", PkgPath: "pkg"}
	_, _ = g.AddNode(ctx, n1)
	_, _ = g.AddNode(ctx, n2)

	e := &parser.Edge{From: "pkg.A", To: "pkg.B", Type: parser.EdgeTypeCalls}
	_ = g.AddEdge(ctx, e)

	server := NewServer(g)

	paramsRaw := []byte(`{"name": "get_dependencies", "arguments": {"node_id": "pkg.A"}}`)
	req := &Request{JSONRPC: "2.0", ID: 4, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	assert.Nil(t, res.Error)

	resultMap, ok := res.Result.(map[string]interface{})
	require.True(t, ok)
	content, ok := resultMap["content"].([]map[string]interface{})
	require.True(t, ok)
	text := content[0]["text"].(string)

	assert.Contains(t, text, "## Dependencies of `pkg.A`")
	assert.Contains(t, text, "**`FuncB`**")
	assert.Contains(t, text, "Found **1** node(s)")
}

func TestHandleCallGetImplementations(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()

	iface := &parser.Node{ID: "pkg.MyInterface", Type: parser.NodeTypeInterface, Name: "MyInterface", PkgPath: "pkg"}
	strct := &parser.Node{ID: "pkg.MyStruct", Type: parser.NodeTypeStruct, Name: "MyStruct", PkgPath: "pkg"}
	_, _ = g.AddNode(ctx, iface)
	_, _ = g.AddNode(ctx, strct)

	e := &parser.Edge{From: "pkg.MyStruct", To: "pkg.MyInterface", Type: parser.EdgeTypeImplements}
	_ = g.AddEdge(ctx, e)

	server := NewServer(g)

	paramsRaw := []byte(`{"name": "get_implementations", "arguments": {"interface_id": "pkg.MyInterface"}}`)
	req := &Request{JSONRPC: "2.0", ID: 5, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	assert.Nil(t, res.Error)

	resultMap := res.Result.(map[string]interface{})
	content := resultMap["content"].([]map[string]interface{})
	text := content[0]["text"].(string)

	assert.Contains(t, text, "## Implementations of `pkg.MyInterface`")
	assert.Contains(t, text, "**`MyStruct`**")
}

func TestHandleCallFindPath(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()

	n1 := &parser.Node{ID: "A", Type: parser.NodeTypeFunc, Name: "FuncA"}
	n2 := &parser.Node{ID: "B", Type: parser.NodeTypeFunc, Name: "FuncB"}
	n3 := &parser.Node{ID: "C", Type: parser.NodeTypeFunc, Name: "FuncC"}
	_, _ = g.AddNode(ctx, n1)
	_, _ = g.AddNode(ctx, n2)
	_, _ = g.AddNode(ctx, n3)

	_ = g.AddEdge(ctx, &parser.Edge{From: "A", To: "B", Type: parser.EdgeTypeCalls})
	_ = g.AddEdge(ctx, &parser.Edge{From: "B", To: "C", Type: parser.EdgeTypeCalls})

	server := NewServer(g)

	paramsRaw := []byte(`{"name": "find_path", "arguments": {"source_id": "A", "target_id": "C"}}`)
	req := &Request{JSONRPC: "2.0", ID: 6, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	assert.Nil(t, res.Error)

	resultMap := res.Result.(map[string]interface{})
	content := resultMap["content"].([]map[string]interface{})
	text := content[0]["text"].(string)

	assert.Contains(t, text, "Shortest Path")
	assert.Contains(t, text, "FuncA")
	assert.Contains(t, text, "FuncC")
	assert.Contains(t, text, "CALLS")
}

func TestHandleCallUnknownTool(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	paramsRaw := []byte(`{"name": "nonexistent_tool", "arguments": {}}`)
	req := &Request{JSONRPC: "2.0", ID: 7, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	assert.NotNil(t, res.Error)
	assert.Contains(t, res.Error.Message, "Unknown tool")
}
