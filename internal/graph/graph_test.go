package graph

import (
	"context"
	"testing"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"

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

func TestBuildFromParseResultsMergesCrossRepoEdges(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	repoA := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "service-a/main.go", Type: parser.NodeTypeFile, Name: "main.go", RepoID: "service-a"},
		},
		Edges: []*parser.Edge{
			{From: "service-a/main.go", To: "example.com/service-b", Type: parser.EdgeTypeImports, RepoID: "service-a"},
		},
	}
	repoB := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "example.com/service-b", Type: parser.NodeTypePackage, Name: "serviceb", PkgPath: "example.com/service-b", RepoID: "service-b"},
		},
	}

	require.NoError(t, g.BuildFromParseResults(ctx, repoA, repoB))

	deps, err := g.Query().GetDependencies("service-a/main.go")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "example.com/service-b", deps[0].ID)
}

func TestLoadFromStoragesResolvesPersistedCrossRepoEdges(t *testing.T) {
	ctx := context.Background()
	storeA, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer storeA.Close()
	storeB, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer storeB.Close()

	repoA := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "example.com/service-a.Main", Type: parser.NodeTypeFunc, Name: "Main", RepoID: "service-a"},
		},
		Edges: []*parser.Edge{
			{From: "example.com/service-a.Main", To: "example.com/service-b.Handle", Type: parser.EdgeTypeCalls, RepoID: "service-a"},
		},
	}
	repoB := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "example.com/service-b.Handle", Type: parser.NodeTypeFunc, Name: "Handle", RepoID: "service-b"},
		},
	}

	require.NoError(t, NewGraph(storeA).BuildFromParseResult(ctx, repoA))
	require.NoError(t, NewGraph(storeB).BuildFromParseResult(ctx, repoB))

	merged := NewGraph(nil)
	require.NoError(t, merged.LoadFromStorages(ctx, storeA, storeB))

	deps, err := merged.Query().GetDependencies("example.com/service-a.Main")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "example.com/service-b.Handle", deps[0].ID)
}

func TestIncrementalWatcherInboundEdgePreservation(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	// Phase 1: Initial load
	pkgA := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "pkgA", Type: parser.NodeTypePackage, Name: "pkgA", PkgPath: "pkgA"},
			{ID: "pkgA.FuncA", Type: parser.NodeTypeFunc, Name: "FuncA", PkgPath: "pkgA"},
		},
		Edges: []*parser.Edge{
			{From: "pkgA.FuncA", To: "pkgB.FuncB", Type: parser.EdgeTypeCalls},
		},
	}
	pkgB := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "pkgB", Type: parser.NodeTypePackage, Name: "pkgB", PkgPath: "pkgB"},
			{ID: "pkgB.FuncB", Type: parser.NodeTypeFunc, Name: "FuncB", PkgPath: "pkgB"},
		},
	}

	require.NoError(t, g.BuildFromParseResults(ctx, pkgA, pkgB))

	// Verify the inbound edge exists initial
	deps, err := g.Query().GetDependencies("pkgA.FuncA")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "pkgB.FuncB", deps[0].ID)

	// Phase 2: Simulating incremental update on Package B
	require.NoError(t, g.RemovePackage(ctx, "pkgB"))

	// 2. Watcher parses pkgB again
	pkgBUpdated := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "pkgB", Type: parser.NodeTypePackage, Name: "pkgB", PkgPath: "pkgB"},
			{ID: "pkgB.FuncB", Type: parser.NodeTypeFunc, Name: "FuncB", PkgPath: "pkgB"},
		},
	}

	// 3. Watcher adds new parse results to graph
	require.NoError(t, g.BuildFromParseResult(ctx, pkgBUpdated))

	// Verify the inbound edge from pkgA to pkgB is STILL PRESERVED!
	deps, err = g.Query().GetDependencies("pkgA.FuncA")
	require.NoError(t, err)
	require.Len(t, deps, 1, "Inbound edge from pkgA to pkgB should be preserved after pkgB incremental update")
	assert.Equal(t, "pkgB.FuncB", deps[0].ID)
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
