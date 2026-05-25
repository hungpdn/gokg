package parser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspace(t *testing.T) {
	// We can test the parser on the parser package itself
	parser := NewParser("gokg")

	ctx := context.Background()
	result, err := parser.ParseWorkspace(ctx, ".")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Check if the current package is found
	foundPkg := false
	foundFunc := false
	for _, n := range result.Nodes {
		if n.Type == NodeTypePackage && n.PkgPath == "github.com/hungpdn/gokg/internal/parser" {
			foundPkg = true
		}
		if n.Type == NodeTypeFunc && n.Name == "ParseWorkspace" {
			foundFunc = true
		}
	}

	assert.True(t, foundPkg, "Should find the parser package itself")
	assert.True(t, foundFunc, "Should find ParseWorkspace function")

	// Check edges
	foundContainsEdge := false
	for _, e := range result.Edges {
		if e.Type == EdgeTypeContains {
			foundContainsEdge = true
			break
		}
	}
	assert.True(t, foundContainsEdge, "Should find at least one CONTAINS edge")
}

func TestParseWorkspaceContextCancel(t *testing.T) {
	parser := NewParser("gokg")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := parser.ParseWorkspace(ctx, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), context.Canceled.Error())
}
