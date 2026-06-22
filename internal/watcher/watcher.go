package watcher

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
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

	mu        sync.Mutex
	updateMu  sync.Mutex
	timers    map[string]*time.Timer
	delay     time.Duration
	runUpdate func(context.Context, func(context.Context) error) error
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
	// Add root and all subdirectories
	err := filepath.WalkDir(w.rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != w.rootDir && shouldSkipWatchDir(d.Name()) {
				return filepath.SkipDir
			}
			return w.watcher.Add(path)
		}
		return nil
	})
	if err != nil {
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

				// If directory created, watch it
				if event.Has(fsnotify.Create) {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						if shouldSkipWatchDir(info.Name()) {
							continue
						}
						if err := w.watcher.Add(event.Name); err != nil {
							log.Printf("Watcher add error for %s: %v", event.Name, err)
						}
						continue
					}
				}

				if !strings.HasSuffix(event.Name, ".go") {
					continue
				}

				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					dir := filepath.Dir(event.Name)
					w.debounce(ctx, dir)
				}

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
	return strings.HasPrefix(name, ".") ||
		name == "vendor" ||
		name == "testdata" ||
		name == "node_modules"
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

func (w *Watcher) updatePackage(ctx context.Context, dir string) {
	w.updateMu.Lock()
	defer w.updateMu.Unlock()

	w.mu.Lock()
	delete(w.timers, dir)
	runUpdate := w.runUpdate
	w.mu.Unlock()

	log.Printf("Detected change in %s, updating graph incrementally...", dir)

	knownPackagePaths := w.g.PackagePathsForDir(dir)
	if handled := w.removePackagesIfNoGoFiles(ctx, dir, knownPackagePaths, runUpdate); handled {
		return
	}

	// Determine the Go package path for this directory
	cfg := &packages.Config{Mode: packages.NeedName, Context: ctx, Dir: dir}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil || len(pkgs) == 0 {
		if handled := w.removePackagesIfNoGoFiles(ctx, dir, knownPackagePaths, runUpdate); handled {
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
		if handled := w.removePackagesIfNoGoFiles(ctx, dir, knownPackagePaths, runUpdate); handled {
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

	// Aggressively release memory back to the OS after a heavy package parse
	debug.FreeOSMemory()
}

func (w *Watcher) removePackagesIfNoGoFiles(
	ctx context.Context,
	dir string,
	pkgPaths []string,
	runUpdate func(context.Context, func(context.Context) error) error,
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
	debug.FreeOSMemory()
	return true
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
