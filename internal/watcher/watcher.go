package watcher

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"golang.org/x/tools/go/packages"
)

type Watcher struct {
	watcher *fsnotify.Watcher
	g       *graph.Graph
	p       *parser.Parser
	rootDir string

	mu             sync.Mutex
	updateMu       sync.Mutex
	timers         map[string]*time.Timer
	structureTimer *time.Timer
	delay          time.Duration
	runUpdate      func(context.Context, func(context.Context) error) error
}

func NewWatcher(g *graph.Graph, p *parser.Parser, rootDir string) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		watcher: w,
		g:       g,
		p:       p,
		rootDir: rootDir,
		timers:  make(map[string]*time.Timer),
		delay:   500 * time.Millisecond,
		runUpdate: func(ctx context.Context, update func(context.Context) error) error {
			return update(ctx)
		},
	}, nil
}

func (w *Watcher) SetUpdateRunner(runUpdate func(context.Context, func(context.Context) error) error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if runUpdate == nil {
		w.runUpdate = func(ctx context.Context, update func(context.Context) error) error {
			return update(ctx)
		}
		return
	}
	w.runUpdate = runUpdate
}

func (w *Watcher) Start(ctx context.Context) error {
	if err := w.addWatchDirRecursive(w.rootDir); err != nil {
		return fmt.Errorf("failed to watch directories: %w", err)
	}

	go func() {
		defer func() {
			if err := w.watcher.Close(); err != nil {
				log.Printf("Watcher close error: %v", err)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				w.handleEvent(ctx, event)

			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				log.Printf("Watcher error: %v", err)
			}
		}
	}()

	return nil
}

func shouldSkipWatchDir(name string) bool {
	return parser.ShouldSkipFolder(name)
}

func (w *Watcher) addWatchDirRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && shouldSkipWatchDir(d.Name()) {
			return filepath.SkipDir
		}
		return w.watcher.Add(path)
	})
}

func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	if event.Has(fsnotify.Create) {
		info, err := os.Stat(event.Name)
		if err == nil {
			if info.IsDir() {
				if shouldSkipWatchDir(info.Name()) {
					return
				}
				if err := w.addWatchDirRecursive(event.Name); err != nil {
					log.Printf("Watcher add error for %s: %v", event.Name, err)
				}
				w.updateTree(ctx, event.Name)
				return
			}
			if !strings.HasSuffix(event.Name, ".go") {
				if parser.ShouldSkipFile(info.Name()) {
					return
				}
				w.debounceRepositoryStructure(ctx)
				return
			}
		}
	}

	if (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) && !strings.HasSuffix(event.Name, ".go") {
		if parser.ShouldSkipFile(filepath.Base(event.Name)) {
			return
		}
		w.removePathAndRefreshStructure(ctx, event.Name)
		return
	}

	if !strings.HasSuffix(event.Name, ".go") {
		return
	}

	if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		dir := filepath.Dir(event.Name)
		w.debounce(ctx, dir)
	}
}

func (w *Watcher) debounce(ctx context.Context, dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if timer, exists := w.timers[dir]; exists {
		timer.Stop()
	}

	w.timers[dir] = time.AfterFunc(w.delay, func() {
		w.updatePackage(ctx, dir)
	})
}

func (w *Watcher) debounceRepositoryStructure(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.structureTimer != nil {
		w.structureTimer.Stop()
	}

	var timer *time.Timer
	timer = time.AfterFunc(w.delay, func() {
		w.mu.Lock()
		if w.structureTimer != timer {
			w.mu.Unlock()
			return
		}
		w.structureTimer = nil
		w.mu.Unlock()

		if ctx.Err() == nil {
			w.refreshRepositoryStructure(ctx)
		}
	})
	w.structureTimer = timer
}

func (w *Watcher) updatePackage(ctx context.Context, dir string) {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()

	w.updatePackageLocked(ctx, dir, true)
}

func (w *Watcher) updatePackageLocked(ctx context.Context, dir string, refreshStructure bool) {
	w.mu.Lock()
	delete(w.timers, dir)
	runUpdate := w.runUpdate
	w.mu.Unlock()

	log.Printf("Detected change in %s, updating graph incrementally...", dir)

	knownPackagePaths := w.g.PackagePathsForDir(dir)
	if handled := w.removePackagesIfNoGoFiles(ctx, dir, knownPackagePaths, runUpdate, refreshStructure); handled {
		return
	}

	// Determine the Go package path for this directory
	cfg := &packages.Config{Mode: packages.NeedName, Context: ctx, Dir: dir}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil || len(pkgs) == 0 {
		if handled := w.removePackagesIfNoGoFiles(ctx, dir, knownPackagePaths, runUpdate, refreshStructure); handled {
			return
		}
		log.Printf("Could not determine package for dir %s: %v", dir, err)
		return
	}
	pkgPath := pkgs[0].PkgPath

	// Reparse before mutating the graph so a transient parse failure does not
	// erase the previous package snapshot. Use the lightweight incremental
	// mode to avoid pulling in all transitive dependencies on every file save.
	res, err := w.p.ParsePackageIncremental(ctx, dir)
	if err != nil {
		if handled := w.removePackagesIfNoGoFiles(ctx, dir, knownPackagePaths, runUpdate, refreshStructure); handled {
			return
		}
		log.Printf("Error reparsing package %s: %v", pkgPath, err)
		return
	}

	if err := runUpdate(ctx, func(ctx context.Context) error {
		if err := w.g.RemovePackage(ctx, pkgPath); err != nil {
			return fmt.Errorf("remove old package nodes: %w", err)
		}
		if err := w.g.BuildFromParseResult(ctx, res); err != nil {
			return fmt.Errorf("build graph from new parse result: %w", err)
		}
		return nil
	}); err != nil {
		log.Printf("Error updating graph for package %s: %v", pkgPath, err)
		return
	}

	log.Printf("Successfully updated graph for package: %s", pkgPath)
	if refreshStructure {
		if err := w.refreshRepositoryStructureLocked(ctx, runUpdate); err != nil {
			log.Printf("Error refreshing repository structure: %v", err)
		}
	}

	// Aggressively release memory back to the OS after a heavy package parse
	debug.FreeOSMemory()
}

func (w *Watcher) removePackagesIfNoGoFiles(
	ctx context.Context,
	dir string,
	pkgPaths []string,
	runUpdate func(context.Context, func(context.Context) error) error,
	refreshStructure bool,
) bool {
	if len(pkgPaths) == 0 {
		return false
	}

	hasGoFiles, err := dirHasGoFiles(dir, w.p.IncludeTests)
	if err != nil {
		log.Printf("Could not inspect dir %s for deleted package cleanup: %v", dir, err)
		return true
	}
	if hasGoFiles {
		return false
	}

	if err := runUpdate(ctx, func(ctx context.Context) error {
		for _, pkgPath := range pkgPaths {
			if err := w.g.RemovePackage(ctx, pkgPath); err != nil {
				return fmt.Errorf("remove deleted package %s: %w", pkgPath, err)
			}
		}
		return nil
	}); err != nil {
		log.Printf("Error removing deleted package snapshot for %s: %v", dir, err)
		return true
	}

	log.Printf("Removed graph snapshot for deleted package directory: %s", dir)
	if refreshStructure {
		if err := w.refreshRepositoryStructureLocked(ctx, runUpdate); err != nil {
			log.Printf("Error refreshing repository structure: %v", err)
		}
	}
	debug.FreeOSMemory()
	return true
}

func (w *Watcher) updateTree(ctx context.Context, root string) {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()

	runUpdate := w.currentUpdateRunner()
	w.updateTreeLocked(ctx, root, runUpdate)
}

func (w *Watcher) refreshRepositoryStructure(ctx context.Context) {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()

	if err := w.refreshRepositoryStructureLocked(ctx, w.currentUpdateRunner()); err != nil {
		log.Printf("Error refreshing repository structure: %v", err)
	}
}

func (w *Watcher) updateTreeLocked(
	ctx context.Context,
	root string,
	runUpdate func(context.Context, func(context.Context) error) error,
) {
	dirs, err := goPackageDirs(root, w.p.IncludeTests)
	if err != nil {
		log.Printf("Could not inspect new directory tree %s: %v", root, err)
	}
	for _, dir := range dirs {
		w.updatePackageLocked(ctx, dir, false)
	}
	if err := w.refreshRepositoryStructureLocked(ctx, runUpdate); err != nil {
		log.Printf("Error refreshing repository structure for %s: %v", root, err)
	}
}

func (w *Watcher) removePathAndRefreshStructure(ctx context.Context, path string) {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()

	runUpdate := w.currentUpdateRunner()
	pkgPaths := w.g.PackagePathsUnderDir(path)
	if len(pkgPaths) == 0 {
		if err := w.refreshRepositoryStructureLocked(ctx, runUpdate); err != nil {
			log.Printf("Error refreshing repository structure for removed path %s: %v", path, err)
		}
		return
	}

	if err := runUpdate(ctx, func(ctx context.Context) error {
		for _, pkgPath := range pkgPaths {
			if err := w.g.RemovePackage(ctx, pkgPath); err != nil {
				return fmt.Errorf("remove deleted package %s: %w", pkgPath, err)
			}
		}
		return nil
	}); err != nil {
		log.Printf("Error removing graph snapshot for deleted path %s: %v", path, err)
	}
	w.updateTreeLocked(ctx, w.rootDir, runUpdate)
	debug.FreeOSMemory()
}

func (w *Watcher) refreshRepositoryStructureLocked(
	ctx context.Context,
	runUpdate func(context.Context, func(context.Context) error) error,
) error {
	packageFolders := w.g.PackageFoldersForRoot(w.rootDir, w.p.RepoID)
	result, err := w.p.BuildRepositoryStructure(ctx, w.rootDir, packageFolders)
	if err != nil {
		return err
	}
	return runUpdate(ctx, func(ctx context.Context) error {
		return w.g.ReplaceRepositoryStructure(ctx, w.p.RepoID, result)
	})
}

func (w *Watcher) currentUpdateRunner() func(context.Context, func(context.Context) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runUpdate
}

func goPackageDirs(root string, includeTests bool) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && shouldSkipWatchDir(d.Name()) {
			return filepath.SkipDir
		}
		hasGoFiles, err := dirHasGoFiles(path, includeTests)
		if err != nil {
			return err
		}
		if hasGoFiles {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

func dirHasGoFiles(dir string, includeTests bool) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".go") && (includeTests || !strings.HasSuffix(name, "_test.go")) {
			return true, nil
		}
	}
	return false, nil
}
