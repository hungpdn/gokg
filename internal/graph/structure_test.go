package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRepositoryStructure(t *testing.T) {
	ctx := context.Background()
	g := NewGraph(nil)

	root := &parser.Node{ID: "folder:.", Type: parser.NodeTypeFolder, Name: "repo", FilePath: "/tmp/repo", RepoID: "repo"}
	internal := &parser.Node{ID: "folder:internal", Type: parser.NodeTypeFolder, Name: "internal", FilePath: "/tmp/repo/internal", RepoID: "repo"}
	pkg := &parser.Node{ID: "example.com/repo/internal", Type: parser.NodeTypePackage, Name: "internal", PkgPath: "example.com/repo/internal", RepoID: "repo"}
	file := &parser.Node{ID: "/tmp/repo/internal/main.go", Type: parser.NodeTypeFile, Name: "main.go", PkgPath: "example.com/repo/internal", FilePath: "/tmp/repo/internal/main.go", RepoID: "repo"}
	require.NoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{root, internal, pkg, file},
		Edges: []*parser.Edge{
			{From: root.ID, To: internal.ID, Type: parser.EdgeTypeContains, RepoID: "repo"},
			{From: internal.ID, To: pkg.ID, Type: parser.EdgeTypeContains, RepoID: "repo"},
			{From: pkg.ID, To: file.ID, Type: parser.EdgeTypeContains, RepoID: "repo"},
		},
	}))

	tree, err := g.Query().GetRepositoryStructure(RepositoryStructureOptions{
		MaxDepth:        4,
		IncludePackages: true,
		IncludeFiles:    true,
	})
	require.NoError(t, err)
	require.NotNil(t, tree)
	require.Equal(t, "repo", tree.Node.Name)
	require.Len(t, tree.Children, 1)
	require.Equal(t, "internal", tree.Children[0].Node.Name)
	require.Len(t, tree.Children[0].Children, 1)
	require.Equal(t, parser.NodeTypePackage, tree.Children[0].Children[0].Node.Type)
	require.Len(t, tree.Children[0].Children[0].Children, 1)
	assert.Equal(t, "main.go", tree.Children[0].Children[0].Children[0].Node.Name)
}

func TestReplaceRepositoryStructureRemovesStaleStructureOnly(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootDir, "new"), 0o755))
	g := NewGraph(nil)

	oldPkg := &parser.Node{ID: "example.com/repo/old", Type: parser.NodeTypePackage, Name: "old", PkgPath: "example.com/repo/old", RepoID: "repo"}
	newPkg := &parser.Node{ID: "example.com/repo/new", Type: parser.NodeTypePackage, Name: "new", PkgPath: "example.com/repo/new", RepoID: "repo"}
	oldFile := &parser.Node{ID: filepath.Join(rootDir, "old", "old.go"), Type: parser.NodeTypeFile, Name: "old.go", PkgPath: oldPkg.ID, FilePath: filepath.Join(rootDir, "old", "old.go"), RepoID: "repo"}
	newFile := &parser.Node{ID: filepath.Join(rootDir, "new", "new.go"), Type: parser.NodeTypeFile, Name: "new.go", PkgPath: newPkg.ID, FilePath: filepath.Join(rootDir, "new", "new.go"), RepoID: "repo"}
	require.NoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "folder:.", Type: parser.NodeTypeFolder, Name: "repo", FilePath: rootDir, RepoID: "repo"},
			{ID: "folder:old", Type: parser.NodeTypeFolder, Name: "old", FilePath: filepath.Join(rootDir, "old"), RepoID: "repo"},
			oldPkg,
			newPkg,
			oldFile,
			newFile,
		},
		Edges: []*parser.Edge{
			{From: "folder:.", To: "folder:old", Type: parser.EdgeTypeContains, RepoID: "repo"},
			{From: "folder:old", To: oldPkg.ID, Type: parser.EdgeTypeContains, RepoID: "repo"},
			{From: oldPkg.ID, To: oldFile.ID, Type: parser.EdgeTypeContains, RepoID: "repo"},
			{From: newPkg.ID, To: newFile.ID, Type: parser.EdgeTypeContains, RepoID: "repo"},
		},
	}))

	p := parser.NewParser("example.com/repo", "repo")
	structure, err := p.BuildRepositoryStructure(ctx, rootDir, map[string]map[string]bool{
		"new": {newPkg.ID: true},
	})
	require.NoError(t, err)
	require.NoError(t, g.ReplaceRepositoryStructure(ctx, "repo", structure))

	require.NotNil(t, g.nodes[g.nodeMap[oldPkg.ID]], "package snapshot should not be removed by structure refresh")
	tree, err := g.Query().GetRepositoryStructure(RepositoryStructureOptions{
		MaxDepth:        3,
		IncludePackages: true,
	})
	require.NoError(t, err)
	require.Len(t, tree.Children, 1)
	assert.Equal(t, "new", tree.Children[0].Node.Name)
	require.Len(t, tree.Children[0].Children, 1)
	assert.Equal(t, newPkg.ID, tree.Children[0].Children[0].Node.ID)
}
