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
		if n.Type == NodeTypeMethod && n.Name == "ParseWorkspace" {
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
	assert.Equal(t, filepath.Base(dir), nodes["folder:."].Name)
	require.NotNil(t, nodes["folder:worker"])
	require.Equal(t, NodeTypeFolder, nodes["folder:worker"].Type)
	assert.Nil(t, nodes["folder:.gokg"])
	assert.Nil(t, nodes["folder:worker/testdata"])
	assert.Nil(t, nodes["folder:vendor"])

	var workerFile *Node
	for _, n := range result.Nodes {
		if n.Type == NodeTypeFile && n.FilePath == filepath.Join(dir, "worker", "worker.go") {
			workerFile = n
			break
		}
	}
	require.NotNil(t, workerFile)
	assert.Equal(t, "worker.go", workerFile.Name)

	pkgID := "example.com/phase9/worker"
	startID := pkgID + ".Start"
	processID := pkgID + ".process"

	require.NotNil(t, nodes[pkgID])
	require.Equal(t, NodeTypePackage, nodes[pkgID].Type)
	require.NotNil(t, nodes[startID])
	require.Equal(t, NodeTypeFunc, nodes[startID].Type)
	var channelID string
	for _, edge := range result.Edges {
		if edge.From == startID && edge.Type == EdgeTypeSendsTo {
			channelID = edge.To
			break
		}
	}
	require.NotEmpty(t, channelID)
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

func TestParseWorkspaceCapturesTopLevelSymbolsAndTests(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/inventory\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "model.go"), `package inventory

type Status string
type Alias = Item

const StatusReady Status = "ready"

var DefaultItem = Item{Status: StatusReady}

type Item struct {
	Status Status
}

func (i Item) Ready() bool {
	return i.Status == StatusReady
}
`)
	writeTestFile(t, filepath.Join(dir, "model_test.go"), `package inventory

import "testing"

func TestReady(t *testing.T) {
	_ = DefaultItem.Ready()
}
`)

	pkgID := "example.com/inventory"
	statusID := pkgID + ".Status"
	aliasID := pkgID + ".Alias"
	statusReadyID := pkgID + ".StatusReady"
	defaultItemID := pkgID + ".DefaultItem"
	itemID := pkgID + ".Item"
	readyID := pkgID + "." + pkgID + ".Item.Ready"
	testReadyID := pkgID + ".TestReady"
	modelFile := filepath.Join(dir, "model.go")
	testFile := filepath.Join(dir, "model_test.go")

	defaultParser := NewParser(pkgID, "test-repo")
	defaultResult, err := defaultParser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)
	defaultNodes := nodesByID(defaultResult)
	assert.Nil(t, defaultNodes[testFile], "test files should be skipped by default")
	assert.Nil(t, defaultNodes[testReadyID], "test functions should be skipped by default")

	parser := NewParser(pkgID, "test-repo").WithTests(true)
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	nodes := nodesByID(result)
	require.NotNil(t, nodes[statusID])
	assert.Equal(t, NodeTypeTypeAlias, nodes[statusID].Type)
	require.NotNil(t, nodes[aliasID])
	assert.Equal(t, NodeTypeTypeAlias, nodes[aliasID].Type)
	require.NotNil(t, nodes[statusReadyID])
	assert.Equal(t, NodeTypeConstant, nodes[statusReadyID].Type)
	require.NotNil(t, nodes[defaultItemID])
	assert.Equal(t, NodeTypeVariable, nodes[defaultItemID].Type)
	require.NotNil(t, nodes[itemID])
	assert.Equal(t, NodeTypeStruct, nodes[itemID].Type)
	require.NotNil(t, nodes[testReadyID])
	assert.Equal(t, NodeTypeFunc, nodes[testReadyID].Type)
	require.NotNil(t, nodes[testFile], "test files should be included in workspace parsing")
	assert.Equal(t, NodeTypeFile, nodes[testFile].Type)

	assert.True(t, hasEdge(result, modelFile, statusID, EdgeTypeContains))
	assert.True(t, hasEdge(result, modelFile, statusReadyID, EdgeTypeContains))
	assert.True(t, hasEdge(result, modelFile, defaultItemID, EdgeTypeContains))
	assert.True(t, hasEdge(result, testFile, testReadyID, EdgeTypeContains))
	assert.True(t, hasEdge(result, aliasID, itemID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, statusReadyID, statusID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, defaultItemID, itemID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, defaultItemID, itemID, EdgeTypeInstantiates))
	assert.True(t, hasEdge(result, defaultItemID, statusReadyID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, testReadyID, readyID, EdgeTypeCalls))
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
	interfaceMethodID := "example.com/test.example.com/test.MyInterface.DoSomething"
	structMethodID := "example.com/test.*example.com/test.MyStruct.DoSomething"
	funcMethodID := "example.com/test.example.com/test.MyFunc.DoSomething"

	require.NotNil(t, nodes[interfaceMethodID], "Interface method signature should be parsed")
	require.NotNil(t, nodes[structMethodID], "Struct method should be parsed")
	require.NotNil(t, nodes[funcMethodID], "Custom func method should be parsed")
	assert.Equal(t, NodeTypeMethod, nodes[interfaceMethodID].Type)
	assert.Equal(t, NodeTypeMethod, nodes[structMethodID].Type)
	assert.Equal(t, NodeTypeMethod, nodes[funcMethodID].Type)

	// Verify IMPLEMENTS edges
	interfaceID := "example.com/test.MyInterface"
	structID := "example.com/test.MyStruct"
	funcTypeID := "example.com/test.MyFunc"

	assert.True(t, hasEdge(result, interfaceID, interfaceMethodID, EdgeTypeContains), "MyInterface contains DoSomething signature")
	assert.True(t, hasEdge(result, structID, interfaceID, EdgeTypeImplements), "MyStruct implements MyInterface")
	assert.True(t, hasEdge(result, funcTypeID, interfaceID, EdgeTypeImplements), "MyFunc implements MyInterface")

	// Verify CALLS edges
	mainID := "example.com/test.main"
	assert.True(t, hasEdge(result, mainID, structMethodID, EdgeTypeCalls), "main calls struct method")
	assert.True(t, hasEdge(result, mainID, funcMethodID, EdgeTypeCalls), "main calls func method")
}

func TestParseWorkspaceCapturesCallOccurrences(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/calls\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

func helper() {}

func main() {
	helper()
	helper()
}
`)

	parser := NewParser("example.com/calls", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	mainID := "example.com/calls.main"
	helperID := "example.com/calls.helper"
	callEdges := edgesBy(result, mainID, helperID, EdgeTypeCalls)
	require.Len(t, callEdges, 2)

	lines := []int{callEdges[0].Occurrences[0].Line, callEdges[1].Occurrences[0].Line}
	assert.ElementsMatch(t, []int{6, 7}, lines)
	for _, edge := range callEdges {
		require.Len(t, edge.Occurrences, 1)
		assert.Equal(t, filepath.Join(dir, "main.go"), edge.Occurrences[0].FilePath)
		assert.Positive(t, edge.Occurrences[0].Column)
	}
}

func TestParseWorkspaceCapturesCallsInsideFuncLiterals(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/closures\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

func helper() {}

func run(fn func()) {}

func main() {
	run(func() {
		helper()
		helper()
	})
}
`)

	parser := NewParser("example.com/closures", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	mainID := "example.com/closures.main"
	helperID := "example.com/closures.helper"
	helperEdges := edgesBy(result, mainID, helperID, EdgeTypeCalls)
	require.Len(t, helperEdges, 2)

	var lines []int
	for _, edge := range helperEdges {
		require.Len(t, edge.Occurrences, 1)
		lines = append(lines, edge.Occurrences[0].Line)
	}
	assert.ElementsMatch(t, []int{9, 10}, lines)
	assert.True(t, hasEdge(result, mainID, "example.com/closures.run", EdgeTypeCalls))
}

func TestParseWorkspaceKeepsGoFuncCallsOnGoroutineNode(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/gofunc\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

func helper() {}

func main() {
	go func() {
		helper()
	}()
}
`)

	parser := NewParser("example.com/gofunc", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	mainID := "example.com/gofunc.main"
	helperID := "example.com/gofunc.helper"
	assert.False(t, hasEdge(result, mainID, helperID, EdgeTypeCalls), "go func body should not be counted again on the parent function")

	var goroutineID string
	for _, node := range result.Nodes {
		if node.Type == NodeTypeGoroutine && strings.HasPrefix(node.ID, mainID+".goroutine_L") {
			goroutineID = node.ID
			break
		}
	}
	require.NotEmpty(t, goroutineID)
	assert.True(t, hasEdge(result, mainID, goroutineID, EdgeTypeSpawns))
	assert.True(t, hasEdge(result, goroutineID, helperID, EdgeTypeCalls))
}

func TestParseWorkspaceUsesDeclarationIdentityForPackageChannels(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/chglobal\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

var ch = make(chan int)

func send() {
	ch <- 1
}

func recv() {
	<-ch
}
`)

	parser := NewParser("example.com/chglobal", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	var channelIDs []string
	for _, node := range result.Nodes {
		if node.Type == NodeTypeChannel && node.Name == "ch (chan int)" {
			channelIDs = append(channelIDs, node.ID)
		}
	}
	require.Len(t, channelIDs, 1)

	sendID := "example.com/chglobal.send"
	recvID := "example.com/chglobal.recv"
	assert.True(t, hasEdge(result, sendID, channelIDs[0], EdgeTypeSendsTo))
	assert.True(t, hasEdge(result, recvID, channelIDs[0], EdgeTypeReceivesFrom))
}

func TestParseWorkspacePropagatesDirectionalChannelArguments(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/chargs\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

type Worker struct{}

func producer(out chan<- int) {
	out <- 1
}

func consumer(in <-chan int) {
	for range in {
	}
}

func (Worker) Run(in <-chan int, out chan<- int) {
	<-in
	out <- 2
}

func main() {
	in := make(chan int)
	out := make(chan int)
	producer(out)
	consumer(in)
	var w Worker
	w.Run(in, out)
}
`)

	parser := NewParser("example.com/chargs", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	inChannelID := channelNodeIDByName(result, "in (chan int)")
	outChannelID := channelNodeIDByName(result, "out (chan int)")
	require.NotEmpty(t, inChannelID)
	require.NotEmpty(t, outChannelID)

	producerID := "example.com/chargs.producer"
	consumerID := "example.com/chargs.consumer"
	runID := "example.com/chargs.example.com/chargs.Worker.Run"

	assert.True(t, hasEdge(result, producerID, outChannelID, EdgeTypeSendsTo))
	assert.True(t, hasEdge(result, consumerID, inChannelID, EdgeTypeReceivesFrom))
	assert.True(t, hasEdge(result, runID, inChannelID, EdgeTypeReceivesFrom))
	assert.True(t, hasEdge(result, runID, outChannelID, EdgeTypeSendsTo))
}

func TestParseWorkspaceDoesNotEmitExternalImplementsEdges(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/extimpl\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

import "fmt"

type Thing struct{}

func (Thing) String() string { return "" }

var _ fmt.Stringer = Thing{}
`)

	parser := NewParser("example.com/extimpl", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	assert.False(t, hasEdge(result, "example.com/extimpl.Thing", "fmt.Stringer", EdgeTypeImplements))
}

func TestParseWorkspaceCapturesCallsInsideTopLevelFuncLiteralInitializer(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/initfunc\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

type command struct {
	Run func()
}

func helper() {}

var runner = command{
	Run: func() {
		helper()
		helper()
	},
}
`)

	parser := NewParser("example.com/initfunc", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	runnerID := "example.com/initfunc.runner"
	helperID := "example.com/initfunc.helper"
	helperEdges := edgesBy(result, runnerID, helperID, EdgeTypeCalls)
	require.Len(t, helperEdges, 2)

	var lines []int
	for _, edge := range helperEdges {
		require.Len(t, edge.Occurrences, 1)
		lines = append(lines, edge.Occurrences[0].Line)
	}
	assert.ElementsMatch(t, []int{11, 12}, lines)
}

func TestParseWorkspaceCapturesInstantiationOccurrences(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/inst\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

type Token struct {
	Type string
}

func next() {
	_ = Token{Type: "a"}
	_ = Token{Type: "b"}
}
`)

	parser := NewParser("example.com/inst", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	nextID := "example.com/inst.next"
	tokenID := "example.com/inst.Token"
	instantiatesEdges := edgesBy(result, nextID, tokenID, EdgeTypeInstantiates)
	require.Len(t, instantiatesEdges, 2)

	var lines []int
	for _, edge := range instantiatesEdges {
		require.Len(t, edge.Occurrences, 1)
		assert.Equal(t, filepath.Join(dir, "main.go"), edge.Occurrences[0].FilePath)
		assert.Positive(t, edge.Occurrences[0].Column)
		lines = append(lines, edge.Occurrences[0].Line)
	}
	assert.ElementsMatch(t, []int{8, 9}, lines)
}

func TestParseWorkspaceCapturesSemanticEdges(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/lvl2\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package main

import (
	"fmt"
	"sync"

	"example.com/lvl2/task"
	"example.com/lvl2/worker"
)

func main() {
	jobs := make(chan task.Task, 5)
	results := make(chan task.Task, 5)
	var wg sync.WaitGroup

	for w := 1; w <= 3; w++ {
		wg.Add(1)
		go worker.New(w).Start(jobs, results, &wg)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for j := 1; j <= 5; j++ {
		jobs <- task.New(j, j*10)
	}
	close(jobs)

	worker.PrintResults(results)

	fmt.Println("done")
}
`)
	writeTestFile(t, filepath.Join(dir, "task", "task.go"), `package task

import "fmt"

type Task struct {
	ID     int
	Value  int
	Result int
}

func New(id int, value int) Task {
	return Task{ID: id, Value: value}
}

func (t Task) Summary() string {
	if t.Result == 0 {
		return fmt.Sprintf("Task %d = %d", t.ID, t.Value)
	}
	return fmt.Sprintf("Task %d = %d -> %d", t.ID, t.Value, t.Result)
}
`)
	writeTestFile(t, filepath.Join(dir, "task", "processor.go"), `package task

func (t Task) Process() Task {
	t.Result = t.Value * 2
	return t
}

func ProcessBatch(tasks []Task) []Task {
	results := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		results = append(results, t.Process())
	}
	return results
}
`)
	writeTestFile(t, filepath.Join(dir, "worker", "worker.go"), `package worker

import (
	"fmt"
	"sync"
	"time"

	"example.com/lvl2/task"
)

type Worker struct {
	ID int
}

func New(id int) *Worker {
	return &Worker{ID: id}
}

func (w *Worker) Start(tasks <-chan task.Task, results chan<- task.Task, wg *sync.WaitGroup) {
	defer wg.Done()

	for t := range tasks {
		fmt.Printf("Worker %d received %s\n", w.ID, t.Summary())
		time.Sleep(150 * time.Millisecond)
		results <- t.Process()
	}
}
`)
	writeTestFile(t, filepath.Join(dir, "worker", "printer.go"), `package worker

import (
	"fmt"

	"example.com/lvl2/task"
)

func PrintResults(results <-chan task.Task) {
	for r := range results {
		fmt.Println("result:", r.Summary())
	}
}
`)

	parser := NewParser("example.com/lvl2", "test-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	nodes := nodesByID(result)
	mainID := "example.com/lvl2.main"
	taskID := "example.com/lvl2/task.Task"
	workerID := "example.com/lvl2/worker.Worker"
	taskNewID := "example.com/lvl2/task.New"
	workerNewID := "example.com/lvl2/worker.New"
	processBatchID := "example.com/lvl2/task.ProcessBatch"
	printResultsID := "example.com/lvl2/worker.PrintResults"
	processID := "example.com/lvl2/task.example.com/lvl2/task.Task.Process"
	summaryID := "example.com/lvl2/task.example.com/lvl2/task.Task.Summary"
	startID := "example.com/lvl2/worker.*example.com/lvl2/worker.Worker.Start"

	require.NotNil(t, nodes[processID])
	require.NotNil(t, nodes[summaryID])
	require.NotNil(t, nodes[startID])
	assert.Equal(t, NodeTypeMethod, nodes[processID].Type)
	assert.Equal(t, NodeTypeMethod, nodes[summaryID].Type)
	assert.Equal(t, NodeTypeMethod, nodes[startID].Type)

	assert.True(t, hasEdge(result, taskID, processID, EdgeTypeContains))
	assert.True(t, hasEdge(result, taskID, summaryID, EdgeTypeContains))
	assert.True(t, hasEdge(result, workerID, startID, EdgeTypeContains))

	assert.True(t, hasEdge(result, mainID, workerNewID, EdgeTypeCalls))
	assert.True(t, hasEdge(result, mainID, taskNewID, EdgeTypeCalls))
	assert.True(t, hasEdge(result, mainID, printResultsID, EdgeTypeCalls))
	assert.True(t, hasEdge(result, processBatchID, processID, EdgeTypeCalls))
	assert.True(t, hasEdge(result, printResultsID, summaryID, EdgeTypeCalls))
	assert.True(t, hasEdge(result, startID, summaryID, EdgeTypeCalls))
	assert.True(t, hasEdge(result, startID, processID, EdgeTypeCalls))

	assert.True(t, hasEdge(result, taskNewID, taskID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, taskNewID, taskID, EdgeTypeInstantiates))
	assert.True(t, hasEdge(result, workerNewID, workerID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, workerNewID, workerID, EdgeTypeInstantiates))
	assert.True(t, hasEdge(result, startID, taskID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, printResultsID, taskID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, processBatchID, taskID, EdgeTypeReferences))

	for _, id := range []string{"fmt", "sync", "time", "fmt.Println", "fmt.Printf", "fmt.Sprintf", "time.Sleep", "sync.*sync.WaitGroup.Add", "sync.*sync.WaitGroup.Done", "sync.*sync.WaitGroup.Wait"} {
		require.NotNil(t, nodes[id], "expected boundary node %s", id)
		assert.Equal(t, NodeTypeBoundary, nodes[id].Type)
	}
	assert.True(t, hasEdge(result, startID, "fmt.Printf", EdgeTypeCalls))
	assert.True(t, hasEdge(result, startID, "time.Sleep", EdgeTypeCalls))
	assert.True(t, hasEdge(result, summaryID, "fmt.Sprintf", EdgeTypeCalls))

	var startGoroutineID string
	for _, n := range result.Nodes {
		if n.Type == NodeTypeGoroutine && strings.HasPrefix(n.ID, mainID+".goroutine_L") && hasEdge(result, n.ID, startID, EdgeTypeCalls) {
			startGoroutineID = n.ID
			break
		}
	}
	require.NotEmpty(t, startGoroutineID)
	assert.True(t, hasEdge(result, mainID, startGoroutineID, EdgeTypeSpawns))
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

func edgesBy(result *ParseResult, from, to string, edgeType EdgeType) []*Edge {
	var edges []*Edge
	for _, edge := range result.Edges {
		if edge.From == from && edge.To == to && edge.Type == edgeType {
			edges = append(edges, edge)
		}
	}
	return edges
}

func channelNodeIDByName(result *ParseResult, name string) string {
	for _, node := range result.Nodes {
		if node.Type == NodeTypeChannel && node.Name == name {
			return node.ID
		}
	}
	return ""
}
