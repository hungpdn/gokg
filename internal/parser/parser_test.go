package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspace(t *testing.T) {
	// We can test the parser on the parser package itself
	parser := NewParser("github.com/hungpdn/gokg")

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

func TestParseWorkspacePhase9Nodes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/phase9\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "worker", "worker.go"), `package worker

func Start(ch chan int) {
	go process(ch)
	ch <- 1
	<-ch
}

func process(ch chan int) {
	ch <- 2
}
`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gokg", "cache"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "worker", "testdata"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor"), 0o755))

	parser := NewParser("example.com/phase9")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	nodes := nodesByID(result)
	require.NotNil(t, nodes["folder:."])
	require.Equal(t, NodeTypeFolder, nodes["folder:."].Type)
	require.NotNil(t, nodes["folder:worker"])
	require.Equal(t, NodeTypeFolder, nodes["folder:worker"].Type)
	assert.Nil(t, nodes["folder:.gokg"])
	assert.Nil(t, nodes["folder:worker/testdata"])
	assert.Nil(t, nodes["folder:vendor"])

	pkgID := "example.com/phase9/worker"
	startID := pkgID + ".Start"
	processID := pkgID + ".process"
	channelID := startID + ".ch"

	require.NotNil(t, nodes[pkgID])
	require.Equal(t, NodeTypePackage, nodes[pkgID].Type)
	require.NotNil(t, nodes[startID])
	require.Equal(t, NodeTypeFunc, nodes[startID].Type)
	require.NotNil(t, nodes[channelID])
	require.Equal(t, NodeTypeChannel, nodes[channelID].Type)
	assert.Equal(t, "ch (chan int)", nodes[channelID].Name)
	assert.True(t, hasEdge(result, "folder:.", "folder:worker", EdgeTypeContains))
	assert.True(t, hasEdge(result, "folder:worker", pkgID, EdgeTypeContains))
	assert.True(t, hasEdge(result, startID, channelID, EdgeTypeSendsTo))
	assert.True(t, hasEdge(result, startID, channelID, EdgeTypeReceivesFrom))

	var goroutineID string
	for _, n := range result.Nodes {
		if n.Type == NodeTypeGoroutine && strings.HasPrefix(n.ID, startID+".goroutine_L") {
			goroutineID = n.ID
			break
		}
	}
	require.NotEmpty(t, goroutineID, "Should create a GOROUTINE node for go statements")
	assert.True(t, hasEdge(result, startID, goroutineID, EdgeTypeSpawns))
	assert.True(t, hasEdge(result, goroutineID, processID, EdgeTypeCalls))
	assert.False(t, hasEdge(result, startID, processID, EdgeTypeSpawns), "SPAWNS should target the goroutine node, not the callee")
}

func TestParseWorkspaceContextCancel(t *testing.T) {
	parser := NewParser("github.com/hungpdn/gokg")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := parser.ParseWorkspace(ctx, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), context.Canceled.Error())
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func nodesByID(result *ParseResult) map[string]*Node {
	nodes := make(map[string]*Node)
	for _, node := range result.Nodes {
		nodes[node.ID] = node
	}
	return nodes
}

func hasEdge(result *ParseResult, from, to string, edgeType EdgeType) bool {
	for _, edge := range result.Edges {
		if edge.From == from && edge.To == to && edge.Type == edgeType {
			return true
		}
	}
	return false
}
