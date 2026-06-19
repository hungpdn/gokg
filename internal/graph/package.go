package graph

import (
	"path/filepath"
	"sort"
)

// PackagePathsForDir returns package paths with source files directly in dir.
func (g *Graph) PackagePathsForDir(dir string) []string {
	targetDir := cleanAbsPath(dir)

	g.mu.RLock()
	defer g.mu.RUnlock()

	seen := make(map[string]struct{})
	for _, node := range g.nodes {
		if node == nil || node.FilePath == "" || node.PkgPath == "" {
			continue
		}
		if cleanAbsPath(filepath.Dir(node.FilePath)) == targetDir {
			seen[node.PkgPath] = struct{}{}
		}
	}

	paths := make([]string, 0, len(seen))
	for pkgPath := range seen {
		paths = append(paths, pkgPath)
	}
	sort.Strings(paths)
	return paths
}

func cleanAbsPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
