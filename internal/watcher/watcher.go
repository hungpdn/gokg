package watcher

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
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
			// Skip hidden directories like .git or .gokg
			if strings.HasPrefix(d.Name(), ".") && path != w.rootDir {
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
		defer w.watcher.Close()
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
						w.watcher.Add(event.Name)
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

	// Determine the Go package path for this directory
	cfg := &packages.Config{Mode: packages.NeedName, Context: ctx, Dir: dir}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil || len(pkgs) == 0 {
		log.Printf("Could not determine package for dir %s: %v", dir, err)
		return
	}
	pkgPath := pkgs[0].PkgPath

	// Reparse before mutating the graph so a transient parse failure does not
	// erase the previous package snapshot.
	res, err := w.p.ParsePackage(ctx, dir)
	if err != nil {
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
}
