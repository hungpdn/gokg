package graph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	require.Len(t, deps, 1)
	assert.Equal(t, "funcB", deps[0].ID)

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

func TestDependenciesAndBlastRadiusUseSemanticDependencyEdges(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	nodes := []*parser.Node{
		{ID: "file.go", Type: parser.NodeTypeFile, Name: "file.go"},
		{ID: "pkg.Func", Type: parser.NodeTypeFunc, Name: "Func"},
		{ID: "pkg.Called", Type: parser.NodeTypeFunc, Name: "Called"},
		{ID: "fmt", Type: parser.NodeTypeBoundary, Name: "fmt"},
		{ID: "pkg.Type", Type: parser.NodeTypeStruct, Name: "Type"},
		{ID: "pkg.Channel", Type: parser.NodeTypeChannel, Name: "ch"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "file.go", To: "pkg.Func", Type: parser.EdgeTypeContains}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Func", To: "pkg.Called", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Func", To: "fmt", Type: parser.EdgeTypeImports}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Func", To: "pkg.Type", Type: parser.EdgeTypeReferences}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Func", To: "pkg.Type", Type: parser.EdgeTypeInstantiates}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Func", To: "pkg.Channel", Type: parser.EdgeTypeSendsTo}))

	deps, err := g.Query().GetDependencies("pkg.Func")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"fmt", "pkg.Called", "pkg.Type"}, graphNodeIDs(deps))

	blast, err := g.Query().GetBlastRadius("pkg.Channel")
	require.NoError(t, err)
	assert.Empty(t, blast, "channel flow edges belong to concurrency queries, not dependency blast radius")
}

func TestQueryMethodsTreatRemovedPackageNodesAsMissing(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	require.NoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "pkg.Func", Type: parser.NodeTypeFunc, Name: "Func", PkgPath: "pkg"},
			{ID: "pkg.Other", Type: parser.NodeTypeFunc, Name: "Other", PkgPath: "pkg"},
		},
		Edges: []*parser.Edge{
			{From: "pkg.Func", To: "pkg.Other", Type: parser.EdgeTypeCalls},
		},
	}))

	require.NoError(t, g.RemovePackage(ctx, "pkg"))

	_, err := g.Query().GetDependencies("pkg.Func")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node not found")

	_, err = g.Query().GetConcurrencyGraph("pkg.Func")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node not found")
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

func TestBuildFromParseResultUsesBatchPersistenceAndMergesEdges(t *testing.T) {
	ctx := context.Background()
	store := newRecordingBatchStorage()
	g := NewGraph(store)

	require.NoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "funcA", Type: parser.NodeTypeFunc, Name: "FuncA"},
			{ID: "funcB", Type: parser.NodeTypeFunc, Name: "FuncB"},
		},
		Edges: []*parser.Edge{
			{
				From: "funcA",
				To:   "funcB",
				Type: parser.EdgeTypeCalls,
				Occurrences: []parser.EdgeOccurrence{
					{FilePath: "main.go", Line: 10, Column: 2},
				},
			},
			{
				From: "funcA",
				To:   "funcB",
				Type: parser.EdgeTypeCalls,
				Occurrences: []parser.EdgeOccurrence{
					{FilePath: "main.go", Line: 12, Column: 2},
				},
			},
		},
	}))

	assert.Zero(t, store.putCalls)
	require.Len(t, store.batches, 2)

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

	deps, err := g.Query().GetDependencies("funcA")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "funcA", deps[0].ID)

	blast, err := g.Query().GetBlastRadius("funcA")
	require.NoError(t, err)
	require.Len(t, blast, 1)
	assert.Equal(t, "funcA", blast[0].ID)
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
	assert.True(t, hasConcurrencyConnection(fromA, "funcB.ch", parser.EdgeTypeReceivesFrom, "via_goroutine"))

	fromB, err := qb.GetConcurrencyGraph("funcB")
	require.NoError(t, err)
	assert.True(t, hasConcurrencyConnection(fromB, "funcA.goroutine_L10", parser.EdgeTypeCalls, "inbound"))
	assert.True(t, hasConcurrencyConnection(fromB, "funcB.ch", parser.EdgeTypeReceivesFrom, "outbound"))
}

func TestConcurrencyGraphIncludesCalledFunctionChannelFlow(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	nodes := []*parser.Node{
		{ID: "main", Type: parser.NodeTypeFunc, Name: "main"},
		{ID: "worker", Type: parser.NodeTypeFunc, Name: "worker"},
		{ID: "main.ch", Type: parser.NodeTypeChannel, Name: "ch (chan int)"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "main", To: "worker", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "worker", To: "main.ch", Type: parser.EdgeTypeReceivesFrom}))

	connections, err := g.Query().GetConcurrencyGraph("main")
	require.NoError(t, err)
	assert.True(t, hasConcurrencyConnection(connections, "main.ch", parser.EdgeTypeReceivesFrom, "via_call"))
}

func TestConcurrencyGraphIncludesDirectGoroutineChannelFlow(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	nodes := []*parser.Node{
		{ID: "main", Type: parser.NodeTypeFunc, Name: "main"},
		{ID: "main.goroutine_L4_C2", Type: parser.NodeTypeGoroutine, Name: "goroutine_L4_C2"},
		{ID: "main.ch", Type: parser.NodeTypeChannel, Name: "ch (chan int)"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "main", To: "main.goroutine_L4_C2", Type: parser.EdgeTypeSpawns}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "main.goroutine_L4_C2", To: "main.ch", Type: parser.EdgeTypeReceivesFrom}))

	connections, err := g.Query().GetConcurrencyGraph("main")
	require.NoError(t, err)
	assert.True(t, hasConcurrencyConnection(connections, "main.goroutine_L4_C2", parser.EdgeTypeSpawns, "outbound"))
	assert.True(t, hasConcurrencyConnection(connections, "main.ch", parser.EdgeTypeReceivesFrom, "via_goroutine"))
}

func TestSearchNodesCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	_, err := g.AddNode(ctx, &parser.Node{ID: "github.com/acme/service.ParseHTTP", Type: parser.NodeTypeFunc, Name: "ParseHTTP"})
	require.NoError(t, err)
	_, err = g.AddNode(ctx, &parser.Node{ID: "github.com/acme/service.Render", Type: parser.NodeTypeFunc, Name: "Render"})
	require.NoError(t, err)

	results, err := g.Query().SearchNodes("parsehttp")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "ParseHTTP", results[0].Name)

	results, err = g.Query().SearchNodes("ACME/SERVICE")
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestSearchNodesReturnsDeterministicBoundedResults(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	for _, id := range []string{"node-c", "node-a", "node-b"} {
		_, err := g.AddNode(ctx, &parser.Node{ID: id, Type: parser.NodeTypeFunc, Name: "Match"})
		require.NoError(t, err)
	}

	results, err := g.Query().SearchNodes("match")
	require.NoError(t, err)
	assert.Equal(t, []string{"node-a", "node-b", "node-c"}, graphNodeIDs(results))
}

func TestGetSourceCodeHandlesLongGeneratedLines(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	filePath := filepath.Join(t.TempDir(), "generated.go")
	longLine := "package generated // " + strings.Repeat("x", 128*1024)
	require.NoError(t, os.WriteFile(filePath, []byte(longLine), 0o644))

	_, err := g.AddNode(ctx, &parser.Node{
		ID:       "generated.LongLine",
		Type:     parser.NodeTypeFunc,
		Name:     "LongLine",
		FilePath: filePath,
		Lines:    [2]int{1, 1},
	})
	require.NoError(t, err)

	code, err := g.Query().GetSourceCode("generated.LongLine")
	require.NoError(t, err)
	assert.Equal(t, longLine+"\n", code)
}

func TestGetSourceCodeRejectsInvalidLineRanges(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	filePath := filepath.Join(t.TempDir(), "main.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package main\n"), 0o644))

	for _, tt := range []struct {
		name  string
		lines [2]int
		want  string
	}{
		{name: "missing", lines: [2]int{}, want: "no line range info"},
		{name: "negative start", lines: [2]int{-1, 1}, want: "no line range info"},
		{name: "end before start", lines: [2]int{2, 1}, want: "invalid line range"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nodeID := "main." + strings.ReplaceAll(tt.name, " ", "_")
			_, err := g.AddNode(ctx, &parser.Node{
				ID:       nodeID,
				Type:     parser.NodeTypeFunc,
				Name:     tt.name,
				FilePath: filePath,
				Lines:    tt.lines,
			})
			require.NoError(t, err)

			_, err = g.Query().GetSourceCode(nodeID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestFindPathTreatsRemovedNodesAsMissing(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	require.NoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "pkgA.FuncA", Type: parser.NodeTypeFunc, Name: "FuncA", PkgPath: "pkgA"},
			{ID: "pkgB.FuncB", Type: parser.NodeTypeFunc, Name: "FuncB", PkgPath: "pkgB"},
		},
		Edges: []*parser.Edge{
			{From: "pkgA.FuncA", To: "pkgB.FuncB", Type: parser.EdgeTypeCalls},
		},
	}))
	require.NoError(t, g.RemovePackage(ctx, "pkgB"))

	_, err := g.Query().FindPath("pkgA.FuncA", "pkgB.FuncB")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target node not found")
}

func TestFindPathUsesCallEdgesOnly(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	for _, node := range []*parser.Node{
		{ID: "A", Type: parser.NodeTypeFunc, Name: "A"},
		{ID: "B", Type: parser.NodeTypeFunc, Name: "B"},
		{ID: "C", Type: parser.NodeTypeFunc, Name: "C"},
		{ID: "D", Type: parser.NodeTypeFunc, Name: "D"},
	} {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "A", To: "B", Type: parser.EdgeTypeContains}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "B", To: "C", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "A", To: "D", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "D", To: "C", Type: parser.EdgeTypeCalls}))

	path, err := g.Query().FindPath("A", "C")
	require.NoError(t, err)
	require.Len(t, path, 3)
	assert.Equal(t, "A", path[0].Node.ID)
	assert.Equal(t, "D", path[1].Node.ID)
	assert.Equal(t, "C", path[2].Node.ID)
	assert.Equal(t, "CALLS", path[0].EdgeType)
	assert.Equal(t, "CALLS", path[1].EdgeType)
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
	defer closeTestStore(t, storeA)
	storeB, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer closeTestStore(t, storeB)

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
	defer closeTestStore(t, store)

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
	err = g.AddEdge(ctx, &parser.Edge{From: "missingA", To: "missingB", Type: parser.EdgeTypeCalls})
	require.ErrorIs(t, err, ErrUnknownEdgeEndpoint)

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
	id := g.nodeMap["pkg"]
	require.NotNil(t, g.nodes[id], "memory graph should remain intact when storage delete fails")
}

func TestGraphPersistsToRepoStores(t *testing.T) {
	ctx := context.Background()
	storeA, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer closeTestStore(t, storeA)
	storeB, err := storage.NewBadgerStorage(t.TempDir())
	require.NoError(t, err)
	defer closeTestStore(t, storeB)

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

func closeTestStore(t *testing.T, store storage.Storage) {
	t.Helper()

	require.NoError(t, store.Close())
}

func graphNodeIDs(nodes []*parser.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node != nil {
			ids = append(ids, node.ID)
		}
	}
	return ids
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

type recordingBatchStorage struct {
	data     map[string][]byte
	putCalls int
	batches  [][]storage.Entry
}

func newRecordingBatchStorage() *recordingBatchStorage {
	return &recordingBatchStorage{data: make(map[string][]byte)}
}

func (r *recordingBatchStorage) Put(ctx context.Context, key []byte, value []byte) error {
	r.putCalls++
	r.data[string(key)] = append([]byte(nil), value...)
	return nil
}

func (r *recordingBatchStorage) PutBatch(ctx context.Context, entries []storage.Entry) error {
	copied := make([]storage.Entry, 0, len(entries))
	for _, entry := range entries {
		key := append([]byte(nil), entry.Key...)
		value := append([]byte(nil), entry.Value...)
		copied = append(copied, storage.Entry{Key: key, Value: value})
		r.data[string(key)] = value
	}
	r.batches = append(r.batches, copied)
	return nil
}

func (r *recordingBatchStorage) Get(ctx context.Context, key []byte) ([]byte, error) {
	value, ok := r.data[string(key)]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func (r *recordingBatchStorage) Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error {
	for key, value := range r.data {
		if err := fn([]byte(key), value); err != nil {
			return err
		}
	}
	return nil
}

func (r *recordingBatchStorage) Delete(ctx context.Context, key []byte) error {
	delete(r.data, string(key))
	return nil
}

func (r *recordingBatchStorage) Close() error {
	return nil
}
