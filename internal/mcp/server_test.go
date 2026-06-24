package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	assert.NotEmpty(t, serverInfo["version"])
}

func TestHandleInitializeNegotiatesProtocolVersion(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{
			name:      "latest supported version",
			requested: latestMCPProtocolVersion,
			want:      latestMCPProtocolVersion,
		},
		{
			name:      "legacy supported version",
			requested: legacyMCPProtocolVersion,
			want:      legacyMCPProtocolVersion,
		},
		{
			name:      "unsupported version falls back to latest",
			requested: "1999-01-01",
			want:      latestMCPProtocolVersion,
		},
		{
			name: "missing version falls back to latest",
			want: latestMCPProtocolVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := graph.NewGraph(nil)
			server := NewServer(g)
			req := &Request{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "initialize",
			}
			if tt.requested != "" {
				req.Params = json.RawMessage(fmt.Sprintf(`{"protocolVersion":%q}`, tt.requested))
			}

			res := server.handleRequest(req)
			require.NotNil(t, res)
			require.Nil(t, res.Error)

			resultMap, ok := res.Result.(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, tt.want, resultMap["protocolVersion"])
		})
	}
}

func TestHandleInitializedNotificationReturnsNoResponse(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	req := &Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	assert.Nil(t, server.handleRequest(req))
}

func TestHandleUnknownRequestReturnsMethodNotFound(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	req := &Request{
		JSONRPC: "2.0",
		ID:      99,
		Method:  "unknown/method",
	}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	require.NotNil(t, res.Error)
	assert.Equal(t, -32601, res.Error.Code)
	assert.Contains(t, res.Error.Message, "Method not found")
}

func TestHandleRequestRejectsInvalidJSONRPCVersion(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	req := &Request{
		JSONRPC: "1.0",
		ID:      100,
		Method:  "initialize",
	}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	require.NotNil(t, res.Error)
	assert.Equal(t, -32600, res.Error.Code)
	assert.Equal(t, "Invalid Request", res.Error.Message)
}

func requireAddNode(t *testing.T, g *graph.Graph, ctx context.Context, node *parser.Node) {
	t.Helper()

	_, err := g.AddNode(ctx, node)
	require.NoError(t, err)
}

func requireAddEdge(t *testing.T, g *graph.Graph, ctx context.Context, edge *parser.Edge) {
	t.Helper()

	require.NoError(t, g.AddEdge(ctx, edge))
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
	assert.Len(t, tools, 10, "Should have 10 tools registered")

	// Verify new tools are present
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool["name"].(string)] = true
	}
	assert.True(t, toolNames["get_implementations"])
	assert.True(t, toolNames["get_source_code"])
	assert.True(t, toolNames["find_path"])
	assert.True(t, toolNames["get_concurrency_graph"])
	assert.True(t, toolNames["get_repository_structure"])
	assert.True(t, toolNames["execute_cypher"])
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
	requireAddNode(t, g, ctx, n1)
	requireAddNode(t, g, ctx, n2)

	e := &parser.Edge{From: "pkg.A", To: "pkg.B", Type: parser.EdgeTypeCalls}
	requireAddEdge(t, g, ctx, e)

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
	requireAddNode(t, g, ctx, iface)
	requireAddNode(t, g, ctx, strct)

	e := &parser.Edge{From: "pkg.MyStruct", To: "pkg.MyInterface", Type: parser.EdgeTypeImplements}
	requireAddEdge(t, g, ctx, e)

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
	requireAddNode(t, g, ctx, n1)
	requireAddNode(t, g, ctx, n2)
	requireAddNode(t, g, ctx, n3)

	requireAddEdge(t, g, ctx, &parser.Edge{From: "A", To: "B", Type: parser.EdgeTypeCalls})
	requireAddEdge(t, g, ctx, &parser.Edge{From: "B", To: "C", Type: parser.EdgeTypeCalls})

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

func TestHandleCallGetConcurrencyGraph(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()

	funcA := &parser.Node{ID: "pkg.A", Type: parser.NodeTypeFunc, Name: "FuncA", PkgPath: "pkg"}
	funcB := &parser.Node{ID: "pkg.B", Type: parser.NodeTypeFunc, Name: "FuncB", PkgPath: "pkg"}
	gr := &parser.Node{ID: "pkg.A.goroutine_L12", Type: parser.NodeTypeGoroutine, Name: "goroutine_L12", PkgPath: "pkg"}
	ch := &parser.Node{ID: "pkg.A.ch", Type: parser.NodeTypeChannel, Name: "ch (chan int)", PkgPath: "pkg"}
	requireAddNode(t, g, ctx, funcA)
	requireAddNode(t, g, ctx, funcB)
	requireAddNode(t, g, ctx, gr)
	requireAddNode(t, g, ctx, ch)
	requireAddEdge(t, g, ctx, &parser.Edge{From: "pkg.A", To: "pkg.A.goroutine_L12", Type: parser.EdgeTypeSpawns})
	requireAddEdge(t, g, ctx, &parser.Edge{From: "pkg.A.goroutine_L12", To: "pkg.B", Type: parser.EdgeTypeCalls})
	requireAddEdge(t, g, ctx, &parser.Edge{From: "pkg.A", To: "pkg.A.ch", Type: parser.EdgeTypeSendsTo})

	server := NewServer(g)

	paramsRaw := []byte(`{"name": "get_concurrency_graph", "arguments": {"node_id": "pkg.A"}}`)
	req := &Request{JSONRPC: "2.0", ID: 8, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	assert.Nil(t, res.Error)

	resultMap := res.Result.(map[string]interface{})
	content := resultMap["content"].([]map[string]interface{})
	text := content[0]["text"].(string)

	assert.Contains(t, text, "## Concurrency Graph of `pkg.A`")
	assert.Contains(t, text, "goroutine_L12")
	assert.Contains(t, text, "ch (chan int)")
	assert.Contains(t, text, "SPAWNS")
	assert.Contains(t, text, "SENDS_TO")
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

func TestHandleCallRepositoryStructureMarkdown(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()
	root := &parser.Node{ID: "folder:.", Type: parser.NodeTypeFolder, Name: "repo", FilePath: "/tmp/repo", RepoID: "repo"}
	internal := &parser.Node{ID: "folder:internal", Type: parser.NodeTypeFolder, Name: "internal", FilePath: "/tmp/repo/internal", RepoID: "repo"}
	pkg := &parser.Node{ID: "example.com/repo/internal", Type: parser.NodeTypePackage, Name: "internal", PkgPath: "example.com/repo/internal", RepoID: "repo"}
	file := &parser.Node{ID: "/tmp/repo/internal/main.go", Type: parser.NodeTypeFile, Name: "main.go", PkgPath: "example.com/repo/internal", FilePath: "/tmp/repo/internal/main.go", RepoID: "repo"}
	requireAddNode(t, g, ctx, root)
	requireAddNode(t, g, ctx, internal)
	requireAddNode(t, g, ctx, pkg)
	requireAddNode(t, g, ctx, file)
	requireAddEdge(t, g, ctx, &parser.Edge{From: "folder:.", To: "folder:internal", Type: parser.EdgeTypeContains, RepoID: "repo"})
	requireAddEdge(t, g, ctx, &parser.Edge{From: "folder:internal", To: "example.com/repo/internal", Type: parser.EdgeTypeContains, RepoID: "repo"})
	requireAddEdge(t, g, ctx, &parser.Edge{From: "example.com/repo/internal", To: "/tmp/repo/internal/main.go", Type: parser.EdgeTypeContains, RepoID: "repo"})

	server := NewServer(g)
	paramsRaw := []byte(`{"name": "get_repository_structure", "arguments": {"include_files": true, "max_depth": 4}}`)
	req := &Request{JSONRPC: "2.0", ID: 10, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	require.Nil(t, res.Error)
	resultMap := res.Result.(map[string]interface{})
	content := resultMap["content"].([]map[string]interface{})
	text := content[0]["text"].(string)

	assert.Contains(t, text, "## Repository Structure")
	assert.Contains(t, text, "`repo/`")
	assert.Contains(t, text, "`internal/`")
	assert.Contains(t, text, "example.com/repo/internal")
	assert.Contains(t, text, "`main.go`")
}

func TestHandleCallRepositoryStructureRejectsUnsafeLimits(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()
	requireAddNode(t, g, ctx, &parser.Node{ID: "folder:.", Type: parser.NodeTypeFolder, Name: "repo"})
	server := NewServer(g)

	for _, paramsRaw := range [][]byte{
		[]byte(`{"name":"get_repository_structure","arguments":{"max_depth":-1}}`),
		[]byte(`{"name":"get_repository_structure","arguments":{"max_depth":33}}`),
		[]byte(`{"name":"get_repository_structure","arguments":{"max_nodes":-1}}`),
		[]byte(`{"name":"get_repository_structure","arguments":{"max_nodes":5001}}`),
	} {
		req := &Request{JSONRPC: "2.0", ID: 11, Method: "tools/call", Params: json.RawMessage(paramsRaw)}
		res := server.handleRequest(req)
		require.NotNil(t, res)
		require.NotNil(t, res.Error)
		assert.Contains(t, res.Error.Message, "must be at")
	}
}

func TestRepositoryStructureLabelEscapesMarkdownNames(t *testing.T) {
	label := repositoryStructureLabel(&parser.Node{
		Type: parser.NodeTypeFolder,
		Name: "docs`draft\nnext",
	})

	assert.Equal(t, "``docs`draft next/`` (`FOLDER`)", label)
}

func TestHandleCallExecuteCypherRequiresLimit(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()
	requireAddNode(t, g, ctx, &parser.Node{ID: "pkg.A", Type: parser.NodeTypeFunc, Name: "A", PkgPath: "pkg"})
	server := NewServer(g)

	paramsRaw := []byte(`{"name": "execute_cypher", "arguments": {"query": "MATCH (n:FUNC) RETURN n.Name"}}`)
	req := &Request{JSONRPC: "2.0", ID: 8, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	require.NotNil(t, res.Error)
	assert.Contains(t, res.Error.Message, "execute_cypher requires LIMIT")
}

func TestHandleCallExecuteCypherWithLimit(t *testing.T) {
	g := graph.NewGraph(nil)
	ctx := context.Background()
	requireAddNode(t, g, ctx, &parser.Node{ID: "pkg.A", Type: parser.NodeTypeFunc, Name: "A", PkgPath: "pkg"})
	server := NewServer(g)

	paramsRaw := []byte(`{"name": "execute_cypher", "arguments": {"query": "MATCH (n:FUNC) RETURN n.Name LIMIT 1"}}`)
	req := &Request{JSONRPC: "2.0", ID: 9, Method: "tools/call", Params: json.RawMessage(paramsRaw)}

	res := server.handleRequest(req)
	require.NotNil(t, res)
	require.Nil(t, res.Error)
	resultMap := res.Result.(map[string]interface{})
	content := resultMap["content"].([]map[string]interface{})
	text := content[0]["text"].(string)

	assert.Contains(t, text, "Cypher Query Results")
	assert.Contains(t, text, `"n.Name": "A"`)
}

func TestMarkdownFenceExpandsForEmbeddedBackticks(t *testing.T) {
	code := "package main\n\nconst sample = `contains ``` fence`\n"

	assert.Equal(t, "````", markdownFence(code))

	text := formatSourceCodeMarkdown("pkg.Sample", code)
	assert.Contains(t, text, "````go\n")
	assert.Contains(t, text, code)
	assert.Contains(t, text, "\n````\n")
}

func TestServeAcceptsLargeStdioMessages(t *testing.T) {
	g := graph.NewGraph(nil)
	server := NewServer(g)

	params, err := json.Marshal(map[string]interface{}{
		"name": "search_nodes",
		"arguments": map[string]string{
			"query": strings.Repeat("a", 70*1024),
		},
	})
	require.NoError(t, err)
	req, err := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      9,
		Method:  "tools/call",
		Params:  params,
	})
	require.NoError(t, err)

	var out bytes.Buffer
	err = server.Serve(context.Background(), bytes.NewReader(append(req, '\n')), &out)
	require.NoError(t, err)

	var res Response
	require.NoError(t, json.Unmarshal(out.Bytes(), &res))
	assert.Nil(t, res.Error)
	assert.EqualValues(t, 9, res.ID)
}
