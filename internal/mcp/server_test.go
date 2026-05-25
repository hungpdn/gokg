package mcp

import (
	"encoding/json"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"

	"github.com/stretchr/testify/assert"
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
	assert.Len(t, tools, 3)
}

func TestHandleCallTool(t *testing.T) {
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
