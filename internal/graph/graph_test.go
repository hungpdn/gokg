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

func TestConcurrencyGraphIncludesGoroutinesAndChannels(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	nodes := []*parser.Node{
		{ID: "funcA", Type: parser.NodeTypeFunc, Name: "FuncA"},
		{ID: "funcB", Type: parser.NodeTypeFunc, Name: "FuncB"},
		{ID: "funcA.goroutine_L10", Type: parser.NodeTypeGoroutine, Name: "goroutine_L10"},
		{ID: "funcA.ch", Type: parser.NodeTypeChannel, Name: "ch (chan int)"},
		{ID: "funcB.ch", Type: parser.NodeTypeChannel, Name: "ch (chan int)"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA", To: "funcA.goroutine_L10", Type: parser.EdgeTypeSpawns}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA.goroutine_L10", To: "funcB", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA", To: "funcA.ch", Type: parser.EdgeTypeSendsTo}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA", To: "funcA.ch", Type: parser.EdgeTypeReceivesFrom}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcB", To: "funcB.ch", Type: parser.EdgeTypeReceivesFrom}))

	qb := g.Query()

	fromA, err := qb.GetConcurrencyGraph("funcA")
	require.NoError(t, err)
	assert.True(t, hasConcurrencyConnection(fromA, "funcA.goroutine_L10", parser.EdgeTypeSpawns, "outbound"))
	assert.True(t, hasConcurrencyConnection(fromA, "funcA.ch", parser.EdgeTypeSendsTo, "outbound"))
	assert.True(t, hasConcurrencyConnection(fromA, "funcA.ch", parser.EdgeTypeReceivesFrom, "outbound"))

	fromB, err := qb.GetConcurrencyGraph("funcB")
	require.NoError(t, err)
	assert.True(t, hasConcurrencyConnection(fromB, "funcA.goroutine_L10", parser.EdgeTypeCalls, "inbound"))
	assert.True(t, hasConcurrencyConnection(fromB, "funcB.ch", parser.EdgeTypeReceivesFrom, "outbound"))
}

func hasConcurrencyConnection(connections []ConcurrencyConnection, nodeID string, edgeType parser.EdgeType, direction string) bool {
	for _, conn := range connections {
		if conn.Node != nil && conn.Edge != nil &&
			conn.Node.ID == nodeID && conn.Edge.Type == edgeType && conn.Direction == direction {
			return true
		}
	}
	return false
}
