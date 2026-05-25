package parser

import (
	"context"
	"fmt"
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
}

func NewParser(modulePrefix string) *Parser {
	return &Parser{ModulePrefix: modulePrefix}
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

	return result, nil
}


