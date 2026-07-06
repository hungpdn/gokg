package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetNodeContextIncludesCoreRelations(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "service.go")
	require.NoError(t, os.WriteFile(filePath, []byte("package pkg\n\nfunc Target() {\n\tDep()\n}\n"), 0o644))

	nodes := []*parser.Node{
		{ID: "pkg", Type: parser.NodeTypePackage, Name: "pkg", PkgPath: "pkg", RepoID: "repo-a"},
		{ID: filePath, Type: parser.NodeTypeFile, Name: "service.go", PkgPath: "pkg", FilePath: filePath, RepoID: "repo-a"},
		{ID: "pkg.Target", Type: parser.NodeTypeFunc, Name: "Target", PkgPath: "pkg", FilePath: filePath, Lines: [2]int{3, 5}, RepoID: "repo-a"},
		{ID: "pkg.Dep", Type: parser.NodeTypeFunc, Name: "Dep", PkgPath: "pkg", FilePath: filePath, Lines: [2]int{7, 9}, RepoID: "repo-a"},
		{ID: "fmt", Type: parser.NodeTypeBoundary, Name: "fmt"},
		{ID: "pkg.Caller", Type: parser.NodeTypeFunc, Name: "Caller", PkgPath: "pkg", RepoID: "repo-a"},
		{ID: "pkg.Second", Type: parser.NodeTypeFunc, Name: "Second", PkgPath: "pkg", RepoID: "repo-a"},
		{ID: "pkg.Register", Type: parser.NodeTypeFunc, Name: "Register", PkgPath: "pkg", RepoID: "repo-a"},
		{ID: "pkg/routes.go::route:GET:/target", Type: parser.NodeTypeRoute, Name: "GET /target", PkgPath: "pkg", FilePath: filePath, Lines: [2]int{11, 11}, RepoID: "repo-a"},
		{ID: "pkg.Target.goroutine_L4", Type: parser.NodeTypeGoroutine, Name: "goroutine", PkgPath: "pkg", RepoID: "repo-a"},
		{ID: "pkg.ch", Type: parser.NodeTypeChannel, Name: "ch", PkgPath: "pkg", RepoID: "repo-a"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}
	edges := []*parser.Edge{
		{From: "pkg", To: filePath, Type: parser.EdgeTypeContains, RepoID: "repo-a"},
		{From: filePath, To: "pkg.Target", Type: parser.EdgeTypeContains, RepoID: "repo-a"},
		{From: "pkg.Target", To: "pkg.Dep", Type: parser.EdgeTypeCalls, RepoID: "repo-a"},
		{From: "pkg.Target", To: "fmt", Type: parser.EdgeTypeImports, RepoID: "repo-a"},
		{From: "pkg.Caller", To: "pkg.Target", Type: parser.EdgeTypeCalls, RepoID: "repo-a"},
		{From: "pkg.Second", To: "pkg.Caller", Type: parser.EdgeTypeReferences, RepoID: "repo-a"},
		{From: "pkg.Register", To: "pkg/routes.go::route:GET:/target", Type: parser.EdgeTypeRegistersRoute, RepoID: "repo-a"},
		{From: "pkg/routes.go::route:GET:/target", To: "pkg.Target", Type: parser.EdgeTypeReferences, RepoID: "repo-a"},
		{From: "pkg.Target", To: "pkg.Target.goroutine_L4", Type: parser.EdgeTypeSpawns, RepoID: "repo-a"},
		{From: "pkg.Target.goroutine_L4", To: "pkg.ch", Type: parser.EdgeTypeReceivesFrom, RepoID: "repo-a"},
	}
	for _, edge := range edges {
		require.NoError(t, g.AddEdge(ctx, edge))
	}

	got, err := g.Query().GetNodeContext("pkg.Target", NodeContextOptions{MaxDepth: 2})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "pkg.Target", got.Node.ID)
	assert.Contains(t, got.SourceCode, "func Target()")
	assert.ElementsMatch(t, []string{"fmt", "pkg.Dep"}, nodeContextRelationNodeIDs(got.Dependencies))
	assert.ElementsMatch(t, []string{"pkg.Caller", "pkg.Second", "pkg/routes.go::route:GET:/target"}, nodeDistanceIDs(got.Dependents))
	assert.ElementsMatch(t, []string{filePath}, nodeContextRelationNodeIDs(got.Parents))
	assert.ElementsMatch(t, []string{"pkg/routes.go::route:GET:/target"}, nodeContextRelationNodeIDs(got.Routes))
	assert.True(t, hasConcurrencyConnection(got.Concurrency, "pkg.Target.goroutine_L4", parser.EdgeTypeSpawns, "outbound"))
	assert.True(t, hasConcurrencyConnection(got.Concurrency, "pkg.ch", parser.EdgeTypeReceivesFrom, "via_goroutine"))
	assert.Empty(t, got.Warnings)
}

func TestGetNodeContextIncludesInterfaceRelations(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)
	for _, node := range []*parser.Node{
		{ID: "pkg.Store", Type: parser.NodeTypeInterface, Name: "Store", PkgPath: "pkg"},
		{ID: "pkg.SQLStore", Type: parser.NodeTypeStruct, Name: "SQLStore", PkgPath: "pkg"},
	} {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.SQLStore", To: "pkg.Store", Type: parser.EdgeTypeImplements}))

	implContext, err := g.Query().GetNodeContext("pkg.SQLStore", NodeContextOptions{IncludeSource: boolPtr(false)})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"pkg.Store"}, nodeContextRelationNodeIDs(implContext.Interfaces))

	ifaceContext, err := g.Query().GetNodeContext("pkg.Store", NodeContextOptions{IncludeSource: boolPtr(false)})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"pkg.SQLStore"}, nodeContextRelationNodeIDs(ifaceContext.Interfaces))
}

func TestGetNodeContextCapsDependenciesAndDependents(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)
	for _, node := range []*parser.Node{
		{ID: "pkg.Target", Type: parser.NodeTypeFunc, Name: "Target"},
		{ID: "pkg.DepA", Type: parser.NodeTypeFunc, Name: "DepA"},
		{ID: "pkg.DepB", Type: parser.NodeTypeFunc, Name: "DepB"},
		{ID: "pkg.CallerA", Type: parser.NodeTypeFunc, Name: "CallerA"},
		{ID: "pkg.CallerB", Type: parser.NodeTypeFunc, Name: "CallerB"},
	} {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Target", To: "pkg.DepA", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Target", To: "pkg.DepB", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.CallerA", To: "pkg.Target", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.CallerB", To: "pkg.Target", Type: parser.EdgeTypeCalls}))

	got, err := g.Query().GetNodeContext("pkg.Target", NodeContextOptions{
		IncludeSource:   boolPtr(false),
		MaxDependencies: 1,
		MaxCallers:      1,
	})
	require.NoError(t, err)
	require.Len(t, got.Dependencies, 1)
	require.Len(t, got.Dependents, 1)
	assert.True(t, got.DependenciesTruncated)
	assert.True(t, got.DependentsTruncated)
	assert.Contains(t, strings.Join(got.Warnings, "\n"), "dependencies truncated")
	assert.Contains(t, strings.Join(got.Warnings, "\n"), "dependents truncated")
}

func TestGetNodeContextSourceWarningAndUnknownNode(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)
	_, err := g.AddNode(ctx, &parser.Node{ID: "pkg", Type: parser.NodeTypePackage, Name: "pkg", PkgPath: "pkg"})
	require.NoError(t, err)

	got, err := g.Query().GetNodeContext("pkg", NodeContextOptions{})
	require.NoError(t, err)
	assert.Contains(t, strings.Join(got.Warnings, "\n"), "source unavailable")

	_, err = g.Query().GetNodeContext("missing", NodeContextOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node not found: missing")
}

func boolPtr(value bool) *bool {
	return &value
}

func nodeContextRelationNodeIDs(relations []NodeContextRelation) []string {
	ids := make([]string, 0, len(relations))
	for _, relation := range relations {
		if relation.Node != nil {
			ids = append(ids, relation.Node.ID)
		}
	}
	return ids
}

func nodeDistanceIDs(nodes []NodeDistance) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Node != nil {
			ids = append(ids, node.Node.ID)
		}
	}
	return ids
}
