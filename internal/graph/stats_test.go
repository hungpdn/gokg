package graph

import (
	"context"
	"testing"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStats(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	nodes := []*parser.Node{
		{ID: "folder", Type: parser.NodeTypeFolder, Name: ".", FilePath: "/tmp/app", RepoID: "repo-a"},
		{ID: "pkg", Type: parser.NodeTypePackage, Name: "pkg", PkgPath: "example.com/app", RepoID: "repo-a"},
		{ID: "file", Type: parser.NodeTypeFile, Name: "main.go", FilePath: "/tmp/app/main.go", PkgPath: "example.com/app", RepoID: "repo-a"},
		{ID: "fn", Type: parser.NodeTypeFunc, Name: "main", FilePath: "/tmp/app/main.go", PkgPath: "example.com/app", RepoID: "repo-a"},
		{ID: "dep", Type: parser.NodeTypeBoundary, Name: "fmt.Println", RepoID: "repo-a"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg", To: "file", Type: parser.EdgeTypeContains, RepoID: "repo-a"}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "fn", To: "dep", Type: parser.EdgeTypeCalls, RepoID: "repo-a"}))

	stats := g.Stats()

	assert.Equal(t, 5, stats.NodeCount)
	assert.Equal(t, 2, stats.EdgeCount)
	assert.Equal(t, 1, stats.FileNodeCount)
	assert.Equal(t, 1, stats.SourceFileCount)
	assert.Positive(t, stats.RAMEstimateBytes)
	assert.Equal(t, 1, stats.NodesByKind["FOLDER"])
	assert.Equal(t, 1, stats.NodesByKind["PACKAGE"])
	assert.Equal(t, 1, stats.NodesByKind["FILE"])
	assert.Equal(t, 1, stats.NodesByKind["FUNC"])
	assert.Equal(t, 1, stats.NodesByKind["BOUNDARY"])
	assert.Equal(t, 1, stats.EdgesByKind["CONTAINS"])
	assert.Equal(t, 1, stats.EdgesByKind["CALLS"])
	assert.Equal(t, 5, stats.NodesByRepo["repo-a"])
	assert.Equal(t, 2, stats.EdgesByRepo["repo-a"])
	require.NotEmpty(t, stats.TopPackagesByNodes)
	assert.Equal(t, "example.com/app", stats.TopPackagesByNodes[0].PkgPath)
	assert.Equal(t, 3, stats.TopPackagesByNodes[0].Nodes)
}
