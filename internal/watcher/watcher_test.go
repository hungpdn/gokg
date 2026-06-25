package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
)

func setupTestGraphAndParser(t *testing.T) (*graph.Graph, *parser.Parser) {
	t.Helper()
	storeDir := t.TempDir()
	store, err := storage.NewBadgerStorage(storeDir)
	if err != nil {
		t.Fatalf("failed to create badger storage: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("failed to close badger storage: %v", err)
		}
	})

	g := graph.NewGraph(store)
	p := parser.NewParser("testmodule", "testmodule")
	return g, p
}

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Write go.mod
	goMod := `module testmodule

go 1.22
`
	err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644)
	if err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	// Write a valid go file
	mainGo := `package main

func main() {}
`
	err = os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0644)
	if err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}

	return dir
}

func TestWatcher_DebounceAndUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := setupTestDir(t)
	mainGoPath := filepath.Join(dir, "main.go")
	g, p := setupTestGraphAndParser(t)
	seedTestPackageSnapshot(t, ctx, g, mainGoPath)
	requireNoError(t, os.Remove(mainGoPath))

	w, err := NewWatcher(g, p, dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer closeTestWatcher(t, w)

	// override delay for fast test
	w.mu.Lock()
	w.delay = 10 * time.Millisecond
	w.mu.Unlock()

	updateCalls := make(chan struct{}, 4)
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		err := update(updateCtx)
		select {
		case updateCalls <- struct{}{}:
		default:
		}
		return err
	})

	// Manually trigger debounce for the directory
	w.debounce(ctx, dir)

	// Wait for the debounce to execute the runUpdate callback
	waitForUpdateCalls(t, updateCalls, 2, 2*time.Second)
}

func TestWatcher_Start_FSNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := setupTestDir(t)
	mainGoPath := filepath.Join(dir, "main.go")
	g, p := setupTestGraphAndParser(t)
	seedTestPackageSnapshot(t, ctx, g, mainGoPath)

	// create hidden dir which should be skipped
	hiddenDir := filepath.Join(dir, ".hidden")
	if err := os.Mkdir(hiddenDir, 0755); err != nil {
		t.Fatalf("failed to create hidden dir: %v", err)
	}

	w, err := NewWatcher(g, p, dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	w.mu.Lock()
	w.delay = 10 * time.Millisecond
	w.mu.Unlock()

	updateCalls := make(chan struct{}, 4)
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		err := update(updateCtx)
		select {
		case updateCalls <- struct{}{}:
		default:
		}
		return err
	})

	if err := w.Start(ctx); err != nil {
		t.Fatalf("expected no error from Start, got %v", err)
	}
	defer closeTestWatcher(t, w)

	// give watcher a moment to start and add directories
	time.Sleep(50 * time.Millisecond)

	// Remove the go file to trigger an fsnotify event without depending on the
	// speed of go/packages on Windows CI.
	requireNoError(t, os.Remove(mainGoPath))

	waitForUpdateCalls(t, updateCalls, 2, 3*time.Second)
}

func TestWatcher_RemovesPackageSnapshotWhenNoGoFilesRemain(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	mainPath := filepath.Join(dir, "main.go")

	g := graph.NewGraph(nil)
	seedTestPackageSnapshot(t, ctx, g, mainPath)

	requireNoError(t, os.Remove(mainPath))

	p := parser.NewParser("testmodule", "testmodule")
	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	called := false
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		called = true
		return update(updateCtx)
	})

	w.updatePackage(ctx, dir)

	if !called {
		t.Fatalf("expected update runner to remove stale package snapshot")
	}
	_, err = g.Query().GetDependencies("testmodule.main")
	if err == nil {
		t.Fatalf("expected removed package node to be missing")
	}
}

func TestWatcher_AddsStructureForGoFileInNewFolder(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	workerDir := filepath.Join(dir, "worker")
	requireNoError(t, os.MkdirAll(workerDir, 0o755))
	requireNoError(t, os.WriteFile(filepath.Join(workerDir, "worker.go"), []byte("package worker\n\nfunc Work() {}\n"), 0o644))

	g := graph.NewGraph(nil)
	p := parser.NewParser("testmodule", "testmodule")
	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	w.updatePackage(ctx, workerDir)

	tree, err := g.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		MaxDepth:        4,
		IncludePackages: true,
	})
	requireNoError(t, err)
	requireTreePath(t, tree, "worker", "testmodule/worker")
}

func TestWatcher_RefreshesStructureForCreatedNonGoFile(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	g := graph.NewGraph(nil)
	p := parser.NewParser("testmodule", "testmodule")

	initial, err := p.BuildRepositoryStructure(ctx, dir, nil)
	requireNoError(t, err)
	requireNoError(t, g.BuildFromParseResult(ctx, initial))

	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	w.mu.Lock()
	w.delay = 10 * time.Millisecond
	w.mu.Unlock()

	updateCalls := make(chan struct{}, 1)
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		err := update(updateCtx)
		select {
		case updateCalls <- struct{}{}:
		default:
		}
		return err
	})

	readmePath := filepath.Join(dir, "README.md")
	requireNoError(t, os.WriteFile(readmePath, []byte("# test\n"), 0o644))
	w.handleEvent(ctx, fsnotify.Event{Name: readmePath, Op: fsnotify.Create})
	waitForUpdateCalls(t, updateCalls, 1, 2*time.Second)

	tree, err := g.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		MaxDepth:     2,
		IncludeFiles: true,
	})
	requireNoError(t, err)
	if !treeContainsName(tree, "README.md") {
		t.Fatalf("expected created non-Go file in repository structure")
	}

	skippedPath := filepath.Join(dir, ".DS_Store")
	requireNoError(t, os.WriteFile(skippedPath, []byte("ignored"), 0o644))
	w.handleEvent(ctx, fsnotify.Event{Name: skippedPath, Op: fsnotify.Create})
	tree, err = g.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		MaxDepth:     2,
		IncludeFiles: true,
	})
	requireNoError(t, err)
	if treeContainsName(tree, ".DS_Store") {
		t.Fatalf("expected skipped file to stay out of repository structure")
	}
}

func TestWatcher_RemovesNonGoFileWithoutReparsingPackages(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	readmePath := filepath.Join(dir, "README.md")
	requireNoError(t, os.WriteFile(readmePath, []byte("# test\n"), 0o644))

	g := graph.NewGraph(nil)
	p := parser.NewParser("testmodule", "testmodule")
	result, err := p.ParseWorkspace(ctx, dir)
	requireNoError(t, err)
	requireNoError(t, g.BuildFromParseResult(ctx, result))

	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	updateCalls := 0
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		updateCalls++
		return update(updateCtx)
	})

	requireNoError(t, os.Remove(readmePath))
	w.handleEvent(ctx, fsnotify.Event{Name: readmePath, Op: fsnotify.Remove})

	if updateCalls != 1 {
		t.Fatalf("expected one structure-only update, got %d", updateCalls)
	}
	tree, err := g.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		MaxDepth:     2,
		IncludeFiles: true,
	})
	requireNoError(t, err)
	if treeContainsName(tree, "README.md") {
		t.Fatalf("expected removed non-Go file to be absent from repository structure")
	}
	if _, err := g.Query().GetDependencies("testmodule.main"); err != nil {
		t.Fatalf("expected package snapshot to remain after non-Go file removal: %v", err)
	}
}

func TestWatcher_IgnoresRemovedSkippedFile(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	skippedPath := filepath.Join(dir, ".DS_Store")
	requireNoError(t, os.WriteFile(skippedPath, []byte("ignored"), 0o644))

	g := graph.NewGraph(nil)
	p := parser.NewParser("testmodule", "testmodule")
	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	updateCalls := 0
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		updateCalls++
		return update(updateCtx)
	})

	requireNoError(t, os.Remove(skippedPath))
	w.handleEvent(ctx, fsnotify.Event{Name: skippedPath, Op: fsnotify.Remove})

	if updateCalls != 0 {
		t.Fatalf("expected skipped file removal to trigger no updates, got %d", updateCalls)
	}
}

func TestWatcher_RemovesStructureForDeletedFolder(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	workerDir := filepath.Join(dir, "worker")
	requireNoError(t, os.MkdirAll(workerDir, 0o755))
	requireNoError(t, os.WriteFile(filepath.Join(workerDir, "worker.go"), []byte("package worker\n\nfunc Work() {}\n"), 0o644))

	g := graph.NewGraph(nil)
	p := parser.NewParser("testmodule", "testmodule")
	result, err := p.ParseWorkspace(ctx, dir)
	requireNoError(t, err)
	requireNoError(t, g.BuildFromParseResult(ctx, result))

	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	requireNoError(t, os.RemoveAll(workerDir))
	w.removePathAndRefreshStructure(ctx, workerDir)

	tree, err := g.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		MaxDepth:        4,
		IncludePackages: true,
	})
	requireNoError(t, err)
	if treeContainsID(tree, "testmodule/worker") || treeContainsName(tree, "worker") {
		t.Fatalf("expected deleted worker folder/package to be absent from repository structure")
	}
	if _, err := g.Query().GetDependencies("testmodule/worker.Work"); err == nil {
		t.Fatalf("expected deleted worker package snapshot to be removed")
	}
}

func TestWatcher_RefreshesStructureForRenamedFolder(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	oldDir := filepath.Join(dir, "worker")
	newDir := filepath.Join(dir, "renamed")
	requireNoError(t, os.MkdirAll(oldDir, 0o755))
	requireNoError(t, os.WriteFile(filepath.Join(oldDir, "worker.go"), []byte("package worker\n\nfunc Work() {}\n"), 0o644))

	g := graph.NewGraph(nil)
	p := parser.NewParser("testmodule", "testmodule")
	result, err := p.ParseWorkspace(ctx, dir)
	requireNoError(t, err)
	requireNoError(t, g.BuildFromParseResult(ctx, result))

	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer closeTestWatcher(t, w)

	requireNoError(t, os.Rename(oldDir, newDir))
	w.removePathAndRefreshStructure(ctx, oldDir)

	tree, err := g.Query().GetRepositoryStructure(graph.RepositoryStructureOptions{
		MaxDepth:        4,
		IncludePackages: true,
	})
	requireNoError(t, err)
	if treeContainsID(tree, "testmodule/worker") || treeContainsID(tree, "folder:worker") {
		t.Fatalf("expected old worker folder/package to be absent after rename")
	}
	requireTreePath(t, tree, "renamed", "testmodule/renamed")
	if _, err := g.Query().GetDependencies("testmodule/renamed.Work"); err != nil {
		t.Fatalf("expected renamed package snapshot to exist: %v", err)
	}
}

func TestShouldSkipWatchDir(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: ".git", want: true},
		{name: ".gokg", want: true},
		{name: "vendor", want: true},
		{name: "testdata", want: true},
		{name: "node_modules", want: true},
		{name: "internal", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipWatchDir(tt.name); got != tt.want {
				t.Fatalf("shouldSkipWatchDir(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func seedTestPackageSnapshot(t *testing.T, ctx context.Context, g *graph.Graph, mainPath string) {
	t.Helper()

	requireNoError(t, g.BuildFromParseResult(ctx, &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "testmodule", Type: parser.NodeTypePackage, Name: "main", PkgPath: "testmodule"},
			{ID: mainPath, Type: parser.NodeTypeFile, Name: "main.go", PkgPath: "testmodule", FilePath: mainPath},
			{ID: "testmodule.main", Type: parser.NodeTypeFunc, Name: "main", PkgPath: "testmodule", FilePath: mainPath},
		},
		Edges: []*parser.Edge{
			{From: "testmodule", To: mainPath, Type: parser.EdgeTypeContains},
			{From: mainPath, To: "testmodule.main", Type: parser.EdgeTypeContains},
		},
	}))
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func waitForUpdateCalls(t *testing.T, updateCalls <-chan struct{}, want int, timeout time.Duration) {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for i := 0; i < want; i++ {
		select {
		case <-updateCalls:
		case <-timer.C:
			t.Fatalf("timeout waiting for watcher update callback %d of %d", i+1, want)
		}
	}
}

func closeTestWatcher(t *testing.T, w *Watcher) {
	t.Helper()

	requireNoError(t, w.watcher.Close())
}

func requireTreePath(t *testing.T, root *graph.RepositoryStructureNode, folderName string, packageID string) {
	t.Helper()
	if root == nil {
		t.Fatalf("repository structure root is nil")
	}
	for _, child := range root.Children {
		if child.Node != nil && child.Node.Name == folderName {
			for _, grandchild := range child.Children {
				if grandchild.Node != nil && grandchild.Node.ID == packageID {
					return
				}
			}
		}
	}
	t.Fatalf("expected repository structure to contain folder %q with package %q", folderName, packageID)
}

func treeContainsID(root *graph.RepositoryStructureNode, id string) bool {
	if root == nil || root.Node == nil {
		return false
	}
	if root.Node.ID == id {
		return true
	}
	for _, child := range root.Children {
		if treeContainsID(child, id) {
			return true
		}
	}
	return false
}

func treeContainsName(root *graph.RepositoryStructureNode, name string) bool {
	if root == nil || root.Node == nil {
		return false
	}
	if root.Node.Name == name {
		return true
	}
	for _, child := range root.Children {
		if treeContainsName(child, name) {
			return true
		}
	}
	return false
}
