package graph

import (
	"context"
	"testing"

	"github.com/hungpdn/gokg/internal/parser"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraphConstructionAndQuery(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil) // No persistent storage for tests

	nodeA := &parser.Node{ID: "funcA", Type: parser.NodeTypeFunc, Name: "FuncA"}
	nodeB := &parser.Node{ID: "funcB", Type: parser.NodeTypeFunc, Name: "FuncB"}
	nodeC := &parser.Node{ID: "chanC", Type: parser.NodeTypeChannel, Name: "chanC"}

	_, err := g.AddNode(ctx, nodeA)
	require.NoError(t, err)

	_, err = g.AddNode(ctx, nodeB)
	require.NoError(t, err)

	_, err = g.AddNode(ctx, nodeC)
	require.NoError(t, err)

	edge1 := &parser.Edge{From: "funcA", To: "funcB", Type: parser.EdgeTypeCalls}
	err = g.AddEdge(ctx, edge1)
	require.NoError(t, err)

	edge2 := &parser.Edge{From: "funcA", To: "chanC", Type: parser.EdgeTypeSendsTo}
	err = g.AddEdge(ctx, edge2)
	require.NoError(t, err)

	qb := g.Query()

	// Test GetDependencies
	deps, err := qb.GetDependencies("funcA")
	require.NoError(t, err)
	assert.Len(t, deps, 2)

	// Test GetBlastRadius
	blast, err := qb.GetBlastRadius("funcB")
	require.NoError(t, err)
	assert.Len(t, blast, 1)
	assert.Equal(t, "funcA", blast[0].ID)

	// Test Concurrency Flow
	flows, err := qb.GetConcurrencyFlow("funcA")
	require.NoError(t, err)
	assert.Len(t, flows, 1)
	assert.Equal(t, "chanC", flows[0].ID)
}
