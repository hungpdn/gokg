package parser

import (
	"context"
	"fmt"
	"go/types"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
)

type ParseResult struct {
	Nodes []*Node
	Edges []*Edge
}

type Parser struct {
	ModulePrefix string
	RepoID       string
	WorkspaceID  string
}

func NewParser(modulePrefix, repoID string) *Parser {
	return &Parser{ModulePrefix: modulePrefix, RepoID: repoID}
}

func NewWorkspaceParser(modulePrefix, repoID, workspaceID string) *Parser {
	return &Parser{ModulePrefix: modulePrefix, RepoID: repoID, WorkspaceID: workspaceID}
}

// ParseWorkspace loads and parses the Go codebase in the given directory.
func (p *Parser) ParseWorkspace(ctx context.Context, dir string) (*ParseResult, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Context: ctx,
		Dir:     dir,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("packages.Load failed: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("encountered errors during package loading")
	}

	result := &ParseResult{
		Nodes: make([]*Node, 0),
		Edges: make([]*Edge, 0),
	}

	p.addWorkspaceHierarchy(result)

	if err := p.buildFolderHierarchy(ctx, dir, pkgs, result); err != nil {
		return nil, err
	}

	var mu sync.Mutex // To protect result slice appends if done concurrently
	g, gCtx := errgroup.WithContext(ctx)

	for _, pkg := range pkgs {
		pkg := pkg // Capture loop variable
		g.Go(func() error {
			if err := gCtx.Err(); err != nil {
				return err
			}

			isInternal := strings.HasPrefix(pkg.PkgPath, p.ModulePrefix)

			node := NewNode()
			node.ID = pkg.PkgPath
			node.Name = pkg.Name
			node.PkgPath = pkg.PkgPath
			node.RepoID = p.RepoID

			if !isInternal {
				node.Type = NodeTypeBoundary

				mu.Lock()
				result.Nodes = append(result.Nodes, node)
				mu.Unlock()
				return nil
			}

			node.Type = NodeTypePackage

			mu.Lock()
			result.Nodes = append(result.Nodes, node)
			mu.Unlock()

			// Extract entities within the package
			if err := p.extractPackageEntities(gCtx, pkg, &mu, result); err != nil {
				return err
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Post-processing: IMPLEMENTS edges
	type structInfo struct {
		id string
		t  types.Type
	}
	type ifaceInfo struct {
		id string
		t  *types.Interface
	}
	var structs []structInfo
	var ifaces []ifaceInfo

	for _, pkg := range pkgs {
		for _, obj := range pkg.TypesInfo.Defs {
			if obj == nil {
				continue
			}
			if typeName, ok := obj.(*types.TypeName); ok {
				if named, ok := typeName.Type().(*types.Named); ok {
					var pkgPath string
					if obj.Pkg() != nil {
						pkgPath = obj.Pkg().Path()
					}
					id := BuildID(pkgPath, ".", obj.Name())

					if _, ok := named.Underlying().(*types.Struct); ok {
						structs = append(structs, structInfo{id: id, t: named})
						structs = append(structs, structInfo{id: id, t: types.NewPointer(named)})
					} else if iType, ok := named.Underlying().(*types.Interface); ok {
						ifaces = append(ifaces, ifaceInfo{id: id, t: iType})
					}
				}
			}
		}
	}

	for _, s := range structs {
		for _, i := range ifaces {
			if i.t.Empty() {
				continue // Skip empty interface "any" or "interface{}"
			}
			if types.Implements(s.t, i.t) {
				edge := NewEdge()
				edge.From = s.id
				edge.To = i.id
				edge.Type = EdgeTypeImplements
				edge.RepoID = p.RepoID
				result.Edges = append(result.Edges, edge)
			}
		}
	}

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
				node.Name = "/"
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
		if !strings.HasPrefix(pkg.PkgPath, p.ModulePrefix) {
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
			packageFolders[folderID][pkg.PkgPath] = true
		}
	}

	return packageFolders
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
func (p *Parser) ParsePackage(ctx context.Context, dir string) (*ParseResult, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Context: ctx,
		Dir:     dir,
	}

	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("packages.Load failed: %w", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("encountered errors during package loading")
	}

	result := &ParseResult{
		Nodes: make([]*Node, 0),
		Edges: make([]*Edge, 0),
	}

	var mu sync.Mutex // For extractPackageEntities

	for _, pkg := range pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		isInternal := strings.HasPrefix(pkg.PkgPath, p.ModulePrefix)

		node := NewNode()
		node.ID = pkg.PkgPath
		node.Name = pkg.Name
		node.PkgPath = pkg.PkgPath
		node.RepoID = p.RepoID

		if !isInternal {
			node.Type = NodeTypeBoundary
			result.Nodes = append(result.Nodes, node)
			continue
		}

		node.Type = NodeTypePackage
		result.Nodes = append(result.Nodes, node)

		if err := p.extractPackageEntities(ctx, pkg, &mu, result); err != nil {
			return nil, err
		}
	}

	// Post-processing: IMPLEMENTS edges
	type structInfo struct {
		id string
		t  types.Type
	}
	type ifaceInfo struct {
		id string
		t  *types.Interface
	}
	var structs []structInfo
	var ifaces []ifaceInfo

	for _, pkg := range pkgs {
		for _, obj := range pkg.TypesInfo.Defs {
			if obj == nil {
				continue
			}
			if typeName, ok := obj.(*types.TypeName); ok {
				if named, ok := typeName.Type().(*types.Named); ok {
					var pkgPath string
					if obj.Pkg() != nil {
						pkgPath = obj.Pkg().Path()
					}
					id := BuildID(pkgPath, ".", obj.Name())

					if _, ok := named.Underlying().(*types.Struct); ok {
						structs = append(structs, structInfo{id: id, t: named})
						structs = append(structs, structInfo{id: id, t: types.NewPointer(named)})
					} else if iType, ok := named.Underlying().(*types.Interface); ok {
						ifaces = append(ifaces, ifaceInfo{id: id, t: iType})
					}
				}
			}
		}
	}

	for _, s := range structs {
		for _, i := range ifaces {
			if i.t.Empty() {
				continue
			}
			if types.Implements(s.t, i.t) {
				edge := NewEdge()
				edge.From = s.id
				edge.To = i.id
				edge.Type = EdgeTypeImplements
				edge.RepoID = p.RepoID
				result.Edges = append(result.Edges, edge)
			}
		}
	}

	return result, nil
}
