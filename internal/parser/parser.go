package parser

import (
	"context"
	"fmt"
	"go/types"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
)

type ParseResult struct {
	Nodes           []*Node
	Edges           []*Edge
	channelArgFlows []channelArgFlow
}

type channelArgFlow struct {
	CalleeID       string
	ParamChannelID string
	ArgChannelID   string
	RepoID         string
}

type Parser struct {
	ModulePrefix string
	RepoID       string
	WorkspaceID  string
	IncludeTests bool
}

func NewParser(modulePrefix, repoID string) *Parser {
	return &Parser{ModulePrefix: modulePrefix, RepoID: repoID}
}

func NewWorkspaceParser(modulePrefix, repoID, workspaceID string) *Parser {
	return &Parser{ModulePrefix: modulePrefix, RepoID: repoID, WorkspaceID: workspaceID}
}

func (p *Parser) WithTests(includeTests bool) *Parser {
	p.IncludeTests = includeTests
	return p
}

// ParseWorkspace loads and parses the Go codebase in the given directory.
func (p *Parser) ParseWorkspace(ctx context.Context, dir string) (*ParseResult, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Context: ctx,
		Dir:     dir,
		Tests:   p.IncludeTests,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load failed: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("encountered errors during package loading")
	}
	pkgs = selectGraphPackages(pkgs)

	result := &ParseResult{
		Nodes: make([]*Node, 0, len(pkgs)*8),
		Edges: make([]*Edge, 0, len(pkgs)*16),
	}

	p.addWorkspaceHierarchy(result)

	if err := p.buildFolderHierarchy(ctx, dir, pkgs, result); err != nil {
		return nil, err
	}

	var mu sync.Mutex // To protect result slice appends if done concurrently
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxPackageWorkers(len(pkgs)))

	for _, pkg := range pkgs {
		pkg := pkg // Capture loop variable
		g.Go(func() error {
			if err := gCtx.Err(); err != nil {
				return err
			}

			local := &ParseResult{
				Nodes: make([]*Node, 0, len(pkg.Syntax)*8+1),
				Edges: make([]*Edge, 0, len(pkg.Syntax)*16),
			}
			pkgPath := packageGraphPath(pkg)
			isInternal := isInternalPackage(pkgPath, p.ModulePrefix)

			node := NewNode()
			node.ID = pkgPath
			node.Name = pkg.Name
			node.PkgPath = pkgPath
			node.RepoID = p.RepoID

			if !isInternal {
				node.Type = NodeTypeBoundary

				mu.Lock()
				result.Nodes = append(result.Nodes, node)
				mu.Unlock()
				return nil
			}

			node.Type = NodeTypePackage

			local.Nodes = append(local.Nodes, node)

			// Extract entities within the package
			if err := p.extractPackageEntities(gCtx, pkg, nil, local); err != nil {
				return err
			}

			mu.Lock()
			result.Nodes = append(result.Nodes, local.Nodes...)
			result.Edges = append(result.Edges, local.Edges...)
			result.channelArgFlows = append(result.channelArgFlows, local.channelArgFlows...)
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	p.resolveChannelArgumentFlowEdges(result)

	// Post-processing: IMPLEMENTS edges
	p.resolveImplementsEdges(pkgs, result)

	return result, nil
}

func (p *Parser) buildFolderHierarchy(ctx context.Context, dir string, pkgs []*packages.Package, result *ParseResult) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}

	folderIDs := make(map[string]bool)

	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		if path != root && shouldSkipFolder(d.Name()) {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		id := p.folderNodeID(rel)
		if !folderIDs[id] {
			folderIDs[id] = true

			node := NewNode()
			node.ID = id
			node.Type = NodeTypeFolder
			node.Name = rel
			if rel == "." {
				node.Name = filepath.Base(root)
			}
			node.FilePath = path
			node.RepoID = p.RepoID
			result.Nodes = append(result.Nodes, node)
		}

		if rel != "." {
			parentRel := filepath.ToSlash(filepath.Dir(rel))
			edge := NewEdge()
			edge.From = p.folderNodeID(parentRel)
			edge.To = id
			edge.Type = EdgeTypeContains
			edge.RepoID = p.RepoID
			result.Edges = append(result.Edges, edge)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("build folder hierarchy: %w", err)
	}

	packageFolders := p.packageFolders(root, pkgs)
	for folderID, pkgPaths := range packageFolders {
		for pkgPath := range pkgPaths {
			edge := NewEdge()
			edge.From = folderID
			edge.To = pkgPath
			edge.Type = EdgeTypeContains
			edge.RepoID = p.RepoID
			result.Edges = append(result.Edges, edge)
		}
	}

	return nil
}

func (p *Parser) packageFolders(root string, pkgs []*packages.Package) map[string]map[string]bool {
	packageFolders := make(map[string]map[string]bool)

	for _, pkg := range pkgs {
		pkgPath := packageGraphPath(pkg)
		if !isInternalPackage(pkgPath, p.ModulePrefix) {
			continue
		}

		files := make([]string, 0, len(pkg.GoFiles)+len(pkg.CompiledGoFiles)+len(pkg.Syntax))
		files = append(files, pkg.GoFiles...)
		files = append(files, pkg.CompiledGoFiles...)
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			if filename != "" {
				files = append(files, filename)
			}
		}

		for _, file := range files {
			absFile := file
			if !filepath.IsAbs(absFile) {
				absFile = filepath.Join(root, absFile)
			}
			absFile, err := filepath.Abs(absFile)
			if err != nil {
				continue
			}

			relDir, err := filepath.Rel(root, filepath.Dir(absFile))
			if err != nil || !isInsideWorkspace(relDir) {
				continue
			}

			folderID := p.folderNodeID(filepath.ToSlash(relDir))
			if packageFolders[folderID] == nil {
				packageFolders[folderID] = make(map[string]bool)
			}
			packageFolders[folderID][pkgPath] = true
		}
	}

	return packageFolders
}

func maxPackageWorkers(pkgCount int) int {
	if pkgCount <= 1 {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		return 1
	}
	if workers > pkgCount {
		return pkgCount
	}
	return workers
}

func shouldSkipFolder(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".")
}

func WorkspaceNodeID(workspaceID string) string {
	return BuildID("workspace:", workspaceID)
}

func RepoNodeID(repoID string) string {
	return BuildID("repo:", repoID)
}

func (p *Parser) addWorkspaceHierarchy(result *ParseResult) {
	if p.WorkspaceID == "" || p.RepoID == "" {
		return
	}

	workspaceID := WorkspaceNodeID(p.WorkspaceID)
	repoID := RepoNodeID(p.RepoID)

	result.Nodes = append(result.Nodes, &Node{
		ID:   workspaceID,
		Type: NodeTypeWorkspace,
		Name: p.WorkspaceID,
	})
	result.Nodes = append(result.Nodes, &Node{
		ID:     repoID,
		Type:   NodeTypeRepo,
		Name:   p.RepoID,
		RepoID: p.RepoID,
	})
	result.Edges = append(result.Edges, &Edge{
		From:   workspaceID,
		To:     repoID,
		Type:   EdgeTypeContains,
		RepoID: p.RepoID,
	})
	result.Edges = append(result.Edges, &Edge{
		From:   repoID,
		To:     p.folderNodeID("."),
		Type:   EdgeTypeContains,
		RepoID: p.RepoID,
	})
}

func (p *Parser) folderNodeID(rel string) string {
	id := folderNodeID(rel)
	if p.WorkspaceID == "" || p.RepoID == "" {
		return id
	}
	return BuildID(RepoNodeID(p.RepoID), ":", id)
}

func folderNodeID(rel string) string {
	if rel == "" || rel == "." {
		return "folder:."
	}
	return BuildID("folder:", filepath.ToSlash(rel))
}

func isInsideWorkspace(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel))
}

// ParsePackage parses a single package directory and returns its entities.
// Prefer ParsePackageIncremental for watch-mode updates.
func (p *Parser) ParsePackage(ctx context.Context, dir string) (*ParseResult, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Context: ctx,
		Dir:     dir,
		Tests:   p.IncludeTests,
	}

	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("packages.Load failed: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("encountered errors during package loading")
	}
	pkgs = selectGraphPackages(pkgs)

	result := &ParseResult{
		Nodes: make([]*Node, 0, len(pkgs)*8),
		Edges: make([]*Edge, 0, len(pkgs)*16),
	}

	for _, pkg := range pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pkgPath := packageGraphPath(pkg)
		isInternal := isInternalPackage(pkgPath, p.ModulePrefix)

		node := NewNode()
		node.ID = pkgPath
		node.Name = pkg.Name
		node.PkgPath = pkgPath
		node.RepoID = p.RepoID

		if !isInternal {
			node.Type = NodeTypeBoundary
			result.Nodes = append(result.Nodes, node)
			continue
		}

		node.Type = NodeTypePackage
		result.Nodes = append(result.Nodes, node)

		if err := p.extractPackageEntities(ctx, pkg, nil, result); err != nil {
			return nil, err
		}
	}

	p.resolveChannelArgumentFlowEdges(result)

	// Post-processing: IMPLEMENTS edges
	p.resolveImplementsEdges(pkgs, result)

	return result, nil
}

// ParsePackageIncremental parses a single package for watch-mode incremental
// updates. It intentionally omits NeedDeps to avoid loading hundreds of
// transitive dependency packages into memory on every file save, keeping the
// RAM footprint low. Cross-package IMPLEMENTS edges are not resolved here as
// they require full dependency info; those edges remain from the last full
// analysis run.
func (p *Parser) ParsePackageIncremental(ctx context.Context, dir string) (*ParseResult, error) {
	cfg := &packages.Config{
		// NeedDeps intentionally omitted — avoids loading all transitive deps.
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Context: ctx,
		Dir:     dir,
		Tests:   p.IncludeTests,
	}

	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("packages.Load failed: %w", err)
	}
	// Tolerate errors from missing dependency type info in incremental mode.
	pkgs = selectGraphPackages(pkgs)
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages found in %s", dir)
	}

	result := &ParseResult{
		Nodes: make([]*Node, 0, len(pkgs)*8),
		Edges: make([]*Edge, 0, len(pkgs)*16),
	}

	for _, pkg := range pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		pkgPath := packageGraphPath(pkg)
		isInternal := isInternalPackage(pkgPath, p.ModulePrefix)

		node := NewNode()
		node.ID = pkgPath
		node.Name = pkg.Name
		node.PkgPath = pkgPath
		node.RepoID = p.RepoID

		if !isInternal {
			node.Type = NodeTypeBoundary
			result.Nodes = append(result.Nodes, node)
			continue
		}

		node.Type = NodeTypePackage
		result.Nodes = append(result.Nodes, node)

		if pkg.TypesInfo != nil {
			if err := p.extractPackageEntities(ctx, pkg, nil, result); err != nil {
				return nil, err
			}
		}
	}

	p.resolveChannelArgumentFlowEdges(result)

	return result, nil
}

type structInfo struct {
	id string
	t  types.Type
}

type ifaceInfo struct {
	id string
	t  *types.Interface
}

func (p *Parser) resolveImplementsEdges(pkgs []*packages.Package, result *ParseResult) {
	var structs []structInfo
	var ifaces []ifaceInfo
	visited := make(map[string]bool)

	var collect func(pkg *packages.Package)
	collect = func(pkg *packages.Package) {
		pkgPath := packageGraphPath(pkg)
		if pkg == nil || pkg.Types == nil || visited[pkgPath] {
			return
		}
		visited[pkgPath] = true

		isWorkspacePkg := isInternalPackage(pkgPath, p.ModulePrefix)
		if !isWorkspacePkg {
			return
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if obj == nil {
				continue
			}
			if typeName, ok := obj.(*types.TypeName); ok {
				if typeName.IsAlias() {
					continue
				}
				if named, ok := typeName.Type().(*types.Named); ok {
					var pkgPath string
					if obj.Pkg() != nil {
						pkgPath = normalizePackagePath(obj.Pkg().Path())
					}
					id := BuildID(pkgPath, ".", obj.Name())

					if iType, ok := named.Underlying().(*types.Interface); ok {
						if iType.Empty() {
							continue
						}
						ifaces = append(ifaces, ifaceInfo{id: id, t: iType})
					} else {
						structs = append(structs, structInfo{id: id, t: named})
						structs = append(structs, structInfo{id: id, t: types.NewPointer(named)})
					}
				}
			}
		}

		for _, imp := range pkg.Imports {
			collect(imp)
		}
	}

	for _, pkg := range pkgs {
		collect(pkg)
	}

	seenImplEdges := make(map[edgeIdentityKey]bool)
	for _, s := range structs {
		for _, i := range ifaces {
			if types.Implements(s.t, i.t) {
				key := edgeIdentityKey{from: s.id, to: i.id, edgeType: EdgeTypeImplements}
				if seenImplEdges[key] {
					continue
				}
				seenImplEdges[key] = true

				edge := NewEdge()
				edge.From = s.id
				edge.To = i.id
				edge.Type = EdgeTypeImplements
				edge.RepoID = p.RepoID
				result.Edges = append(result.Edges, edge)
			}
		}
	}
}

func selectGraphPackages(pkgs []*packages.Package) []*packages.Package {
	selected := make(map[string]*packages.Package)
	order := make([]string, 0, len(pkgs))

	for _, pkg := range pkgs {
		if pkg == nil || isSyntheticTestPackage(pkg) {
			continue
		}

		pkgPath := packageGraphPath(pkg)
		if pkgPath == "" {
			continue
		}

		if _, ok := selected[pkgPath]; !ok {
			order = append(order, pkgPath)
			selected[pkgPath] = pkg
			continue
		}
		if shouldPreferGraphPackage(pkg, selected[pkgPath]) {
			selected[pkgPath] = pkg
		}
	}

	result := make([]*packages.Package, 0, len(order))
	for _, pkgPath := range order {
		result = append(result, selected[pkgPath])
	}
	return result
}

func shouldPreferGraphPackage(candidate, current *packages.Package) bool {
	if current == nil {
		return true
	}
	if candidate.ForTest != "" && current.ForTest == "" {
		return true
	}
	return len(candidate.Syntax) > len(current.Syntax)
}

func isSyntheticTestPackage(pkg *packages.Package) bool {
	if pkg == nil {
		return false
	}
	pkgPath := normalizePackagePath(pkg.PkgPath)
	return strings.HasSuffix(pkgPath, ".test")
}

func packageGraphPath(pkg *packages.Package) string {
	if pkg == nil {
		return ""
	}
	return normalizePackagePath(pkg.PkgPath)
}

func normalizePackagePath(pkgPath string) string {
	if idx := strings.Index(pkgPath, " ["); idx > 0 && strings.HasSuffix(pkgPath, "]") {
		return pkgPath[:idx]
	}
	return pkgPath
}

func isInternalPackage(pkgPath, modulePrefix string) bool {
	pkgPath = normalizePackagePath(pkgPath)
	modulePrefix = strings.TrimSpace(modulePrefix)
	if modulePrefix == "" {
		return false
	}
	return pkgPath == modulePrefix || strings.HasPrefix(pkgPath, modulePrefix+"/")
}
