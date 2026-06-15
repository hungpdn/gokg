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
	withGoBuildCache(t)

	// We can test the parser on the parser package itself
	parser := NewParser("github.com/hungpdn/gokg", "test-repo")

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
	withGoBuildCache(t)

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

	parser := NewParser("example.com/phase9", "test-repo")
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

func TestParseWorkspaceAddsWorkspaceRepoHierarchy(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/service-a\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")

	parser := NewWorkspaceParser("example.com/service-a", "service-a", "demo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	nodes := nodesByID(result)
	workspaceID := WorkspaceNodeID("demo")
	repoID := RepoNodeID("service-a")
	rootFolderID := repoID + ":folder:."

	require.NotNil(t, nodes[workspaceID])
	assert.Equal(t, NodeTypeWorkspace, nodes[workspaceID].Type)
	require.NotNil(t, nodes[repoID])
	assert.Equal(t, NodeTypeRepo, nodes[repoID].Type)
	require.NotNil(t, nodes[rootFolderID])
	assert.Equal(t, NodeTypeFolder, nodes[rootFolderID].Type)

	assert.True(t, hasEdge(result, workspaceID, repoID, EdgeTypeContains))
	assert.True(t, hasEdge(result, repoID, rootFolderID, EdgeTypeContains))
	assert.True(t, hasEdge(result, rootFolderID, "example.com/service-a", EdgeTypeContains))
}

func TestInternalPackageBoundary(t *testing.T) {
	assert.True(t, isInternalPackage("example.com/foo", "example.com/foo"))
	assert.True(t, isInternalPackage("example.com/foo/internal/bar", "example.com/foo"))
	assert.False(t, isInternalPackage("example.com/foo2", "example.com/foo"))
	assert.False(t, isInternalPackage("example.com/foobar/pkg", "example.com/foo"))
	assert.False(t, isInternalPackage("example.com/foo", ""))
}

func TestParseWorkspaceContextCancel(t *testing.T) {
	withGoBuildCache(t)

	parser := NewParser("github.com/hungpdn/gokg", "test-repo")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := parser.ParseWorkspace(ctx, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), context.Canceled.Error())
}

func TestParseMethodCallsAndImplements(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

type MyInterface interface {
	DoSomething()
}

type MyStruct struct{}

func (s *MyStruct) DoSomething() {}

type MyFunc func()
func (f MyFunc) DoSomething() {}

func main() {
	var s MyStruct
	s.DoSomething()

	var f MyFunc = func() {}
	f.DoSomething()
}
`)

	parser := NewParser("example.com/test", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	nodes := nodesByID(result)

	// Verify method nodes
	structMethodID := "example.com/test.*example.com/test.MyStruct.DoSomething"
	funcMethodID := "example.com/test.example.com/test.MyFunc.DoSomething"

	require.NotNil(t, nodes[structMethodID], "Struct method should be parsed")
	require.NotNil(t, nodes[funcMethodID], "Custom func method should be parsed")

	// Verify IMPLEMENTS edges
	interfaceID := "example.com/test.MyInterface"
	structID := "example.com/test.MyStruct"
	funcTypeID := "example.com/test.MyFunc"

	assert.True(t, hasEdge(result, structID, interfaceID, EdgeTypeImplements), "MyStruct implements MyInterface")
	assert.True(t, hasEdge(result, funcTypeID, interfaceID, EdgeTypeImplements), "MyFunc implements MyInterface")

	// Verify CALLS edges
	mainID := "example.com/test.main"
	assert.True(t, hasEdge(result, mainID, structMethodID, EdgeTypeCalls), "main calls struct method")
	assert.True(t, hasEdge(result, mainID, funcMethodID, EdgeTypeCalls), "main calls func method")
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
}

func withGoBuildCache(t *testing.T) {
	t.Helper()

	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
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
