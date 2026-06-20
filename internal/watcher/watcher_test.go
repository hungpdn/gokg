package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	g, p := setupTestGraphAndParser(t)

	w, err := NewWatcher(g, p, dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer w.watcher.Close()

	// override delay for fast test
	w.mu.Lock()
	w.delay = 10 * time.Millisecond
	w.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)

	called := false
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		called = true
		err := update(updateCtx)
		wg.Done()
		return err
	})

	// Manually trigger debounce for the directory
	w.debounce(ctx, dir)

	// Wait for the debounce to execute the runUpdate callback
	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		if !called {
			t.Errorf("expected runUpdate to be called")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for debounce to trigger runUpdate")
	}
}

func TestWatcher_Start_FSNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := setupTestDir(t)
	g, p := setupTestGraphAndParser(t)

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

	var wg sync.WaitGroup
	wg.Add(1)

	called := false
	w.SetUpdateRunner(func(updateCtx context.Context, update func(context.Context) error) error {
		called = true
		err := update(updateCtx)
		wg.Done()
		return err
	})

	if err := w.Start(ctx); err != nil {
		t.Fatalf("expected no error from Start, got %v", err)
	}
	defer w.watcher.Close()

	// give watcher a moment to start and add directories
	time.Sleep(50 * time.Millisecond)

	// Modify the go file to trigger a write event
	mainGoPath := filepath.Join(dir, "main.go")
	f, err := os.OpenFile(mainGoPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open main.go: %v", err)
	}
	if _, err := f.WriteString("\n// modified\n"); err != nil {
		t.Fatalf("failed to append to main.go: %v", err)
	}
	f.Close()

	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		if !called {
			t.Errorf("expected runUpdate to be called by fsnotify event")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for fsnotify event to trigger runUpdate")
	}
}

func TestWatcher_RemovesPackageSnapshotWhenNoGoFilesRemain(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	mainPath := filepath.Join(dir, "main.go")

	g := graph.NewGraph(nil)
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

	requireNoError(t, os.Remove(mainPath))

	p := parser.NewParser("testmodule", "testmodule")
	w, err := NewWatcher(g, p, dir)
	requireNoError(t, err)
	defer w.watcher.Close()

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

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
