package graph

import (
	"context"
	"errors"
	"strings"
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

func TestAddEdgeMergesCallOccurrences(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	_, err := g.AddNode(ctx, &parser.Node{ID: "funcA", Type: parser.NodeTypeFunc, Name: "FuncA"})
	require.NoError(t, err)
	_, err = g.AddNode(ctx, &parser.Node{ID: "funcB", Type: parser.NodeTypeFunc, Name: "FuncB"})
	require.NoError(t, err)

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{
		From: "funcA",
		To:   "funcB",
		Type: parser.EdgeTypeCalls,
		Occurrences: []parser.EdgeOccurrence{
			{FilePath: "main.go", Line: 10, Column: 2},
		},
	}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{
		From: "funcA",
		To:   "funcB",
		Type: parser.EdgeTypeCalls,
		Occurrences: []parser.EdgeOccurrence{
			{FilePath: "main.go", Line: 12, Column: 2},
		},
	}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{
		From: "funcA",
		To:   "funcB",
		Type: parser.EdgeTypeCalls,
		Occurrences: []parser.EdgeOccurrence{
			{FilePath: "main.go", Line: 12, Column: 2},
		},
	}))

	fromID := g.nodeMap["funcA"]
	toID := g.nodeMap["funcB"]
	require.Len(t, g.edges[fromID][toID], 1)
	assert.ElementsMatch(t, []parser.EdgeOccurrence{
		{FilePath: "main.go", Line: 10, Column: 2},
		{FilePath: "main.go", Line: 12, Column: 2},
	}, g.edges[fromID][toID][0].Occurrences)
}

func TestAddEdgePreservesSelfEdgesForExport(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	_, err := g.AddNode(ctx, &parser.Node{ID: "funcA", Type: parser.NodeTypeFunc, Name: "FuncA"})
	require.NoError(t, err)

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{
		From: "funcA",
		To:   "funcA",
		Type: parser.EdgeTypeCalls,
		Occurrences: []parser.EdgeOccurrence{
			{FilePath: "main.go", Line: 10, Column: 2},
		},
	}))

	nodeID := g.nodeMap["funcA"]
	require.Len(t, g.edges[nodeID][nodeID], 1)
	assert.Equal(t, parser.EdgeTypeCalls, g.edges[nodeID][nodeID][0].Type)
	assert.Contains(t, g.ExportDot(), `"funcA" -> "funcA" [label="CALLS", occurrences="1", lines="main.go:10:2"];`)
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

func TestPersistedEdgeKeysDoNotCollideWithColonIDs(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	edgeA := &parser.Edge{From: "a:b", To: "c", Type: parser.EdgeTypeCalls}
	edgeB := &parser.Edge{From: "a", To: "b:c", Type: parser.EdgeTypeCalls}
	require.Equal(t, string(legacyEdgeStorageKey(edgeA)), string(legacyEdgeStorageKey(edgeB)))
	require.NotEqual(t, string(edgeStorageKey(edgeA)), string(edgeStorageKey(edgeB)))

	g := NewGraph(store)
	result := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "a:b", Type: parser.NodeTypeFunc, Name: "A"},
			{ID: "c", Type: parser.NodeTypeFunc, Name: "C"},
			{ID: "a", Type: parser.NodeTypeFunc, Name: "A"},
			{ID: "b:c", Type: parser.NodeTypeFunc, Name: "B"},
		},
		Edges: []*parser.Edge{edgeA, edgeB},
	}

	require.NoError(t, g.BuildFromParseResult(ctx, result))

	var edgeKeys int
	require.NoError(t, store.Iterate(ctx, func(key []byte, value []byte) error {
		if strings.HasPrefix(string(key), "edge:") {
			edgeKeys++
		}
		return nil
	}))
	assert.Equal(t, 2, edgeKeys)
}

func TestGraphReturnsPersistenceErrors(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("disk full")

	g := NewGraph(failingStorage{putErr: wantErr})
	_, err := g.AddNode(ctx, &parser.Node{ID: "A", Type: parser.NodeTypeFunc, Name: "A"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist node")

	g = NewGraph(nil)
	_, err = g.AddNode(ctx, &parser.Node{ID: "A", Type: parser.NodeTypeFunc, Name: "A"})
	require.NoError(t, err)
	_, err = g.AddNode(ctx, &parser.Node{ID: "B", Type: parser.NodeTypeFunc, Name: "B"})
	require.NoError(t, err)
	g.SetStore(failingStorage{putErr: wantErr})
	err = g.AddEdge(ctx, &parser.Edge{From: "A", To: "B", Type: parser.EdgeTypeCalls})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist edge")

	g = NewGraph(nil)
	require.NoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "pkg", Type: parser.NodeTypePackage, Name: "pkg", PkgPath: "pkg"},
		},
	}))
	g.SetStore(failingStorage{deleteErr: wantErr})
	err = g.RemovePackage(ctx, "pkg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete node")
}

func TestGraphPersistsToRepoStores(t *testing.T) {
	ctx := context.Background()
	storeA, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer storeA.Close()
	storeB, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer storeB.Close()

	g := NewGraph(nil)
	g.SetRepoStore("service-a", storeA)
	g.SetRepoStore("service-b", storeB)

	edge := &parser.Edge{From: "example.com/service-a.Work", To: "example.com/service-b.Handle", Type: parser.EdgeTypeCalls, RepoID: "service-a"}
	result := &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "example.com/service-a.Work", Type: parser.NodeTypeFunc, Name: "Work", PkgPath: "example.com/service-a", RepoID: "service-a"},
			{ID: "example.com/service-b.Handle", Type: parser.NodeTypeFunc, Name: "Handle", PkgPath: "example.com/service-b", RepoID: "service-b"},
		},
		Edges: []*parser.Edge{edge},
	}

	require.NoError(t, g.BuildFromParseResult(ctx, result))

	_, err = storeA.Get(ctx, []byte("node:example.com/service-a.Work"))
	require.NoError(t, err)
	_, err = storeB.Get(ctx, []byte("node:example.com/service-a.Work"))
	assert.Error(t, err)

	_, err = storeB.Get(ctx, []byte("node:example.com/service-b.Handle"))
	require.NoError(t, err)
	_, err = storeA.Get(ctx, edgeStorageKey(edge))
	require.NoError(t, err)
	_, err = storeB.Get(ctx, edgeStorageKey(edge))
	assert.Error(t, err)

	require.NoError(t, g.RemovePackage(ctx, "example.com/service-a"))
	_, err = storeA.Get(ctx, []byte("node:example.com/service-a.Work"))
	assert.Error(t, err)
	_, err = storeA.Get(ctx, edgeStorageKey(edge))
	assert.Error(t, err)
	_, err = storeB.Get(ctx, []byte("node:example.com/service-b.Handle"))
	require.NoError(t, err)
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

type failingStorage struct {
	putErr    error
	deleteErr error
}

func (f failingStorage) Put(ctx context.Context, key []byte, value []byte) error {
	return f.putErr
}

func (f failingStorage) Get(ctx context.Context, key []byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (f failingStorage) Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error {
	return errors.New("not implemented")
}

func (f failingStorage) Delete(ctx context.Context, key []byte) error {
	return f.deleteErr
}

func (f failingStorage) Close() error {
	return nil
}
