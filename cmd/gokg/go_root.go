package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

type goAnalysisRoot struct {
	Dir          string
	ModulePrefix string
}

func resolveGoAnalysisRoot(start string) (goAnalysisRoot, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return goAnalysisRoot{}, fmt.Errorf("resolve Go analysis root %q: %w", start, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return goAnalysisRoot{}, fmt.Errorf("inspect Go analysis root %q: %w", start, err)
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}

	if root, ok := nearestModuleRoot(abs); ok {
		return root, nil
	}

	roots, err := nestedModuleRoots(abs)
	if err != nil {
		return goAnalysisRoot{}, err
	}
	switch len(roots) {
	case 0:
		return goAnalysisRoot{Dir: abs}, nil
	case 1:
		return roots[0], nil
	default:
		return goAnalysisRoot{}, fmt.Errorf(
			"multiple Go modules found under %s (%s); run gokg analyze from one module directory or add them to a gokg workspace",
			abs,
			strings.Join(relativeRootDirs(abs, roots), ", "),
		)
	}
}

func nearestModuleRoot(dir string) (goAnalysisRoot, bool) {
	for {
		if hasGoMod(dir) {
			return goAnalysisRoot{Dir: dir, ModulePrefix: detectModulePrefix(dir)}, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return goAnalysisRoot{}, false
		}
		dir = parent
	}
}

func nestedModuleRoots(root string) ([]goAnalysisRoot, error) {
	var roots []goAnalysisRoot

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}

		if path != root && shouldSkipGoRootSearchDir(d.Name()) {
			return filepath.SkipDir
		}

		if hasGoMod(path) {
			roots = append(roots, goAnalysisRoot{Dir: path, ModulePrefix: detectModulePrefix(path)})
			if path != root {
				return filepath.SkipDir
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find Go modules under %s: %w", root, err)
	}

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Dir < roots[j].Dir
	})
	return roots, nil
}

func shouldSkipGoRootSearchDir(name string) bool {
	return strings.HasPrefix(name, ".") ||
		name == "vendor" ||
		name == "testdata" ||
		name == "node_modules"
}

func hasGoMod(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil && !info.IsDir()
}

func detectModulePrefix(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil || f.Module == nil {
		return ""
	}
	return strings.TrimSpace(f.Module.Mod.Path)
}

func relativeRootDirs(base string, roots []goAnalysisRoot) []string {
	dirs := make([]string, 0, len(roots))
	for _, root := range roots {
		rel, err := filepath.Rel(base, root.Dir)
		if err != nil {
			dirs = append(dirs, root.Dir)
			continue
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	return dirs
}
