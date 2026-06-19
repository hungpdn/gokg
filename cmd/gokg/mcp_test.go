package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMCPHTTPURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8080/mcp", mcpHTTPURL("", ""))
	assert.Equal(t, "http://127.0.0.1:9090/mcp", mcpHTTPURL(":9090", "mcp"))
	assert.Equal(t, "http://0.0.0.0:8080/api/mcp", mcpHTTPURL("0.0.0.0:8080", "/api/mcp"))
}
