package parser

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

func (p *Parser) extractPackageEntities(ctx context.Context, pkg *packages.Package, mu *sync.Mutex, result *ParseResult) error {
	for _, file := range pkg.Syntax {
		if err := ctx.Err(); err != nil {
			return err
		}

		pos := pkg.Fset.Position(file.Pos())
		filename := pos.Filename

		fileNode := NewNode()
		fileNode.ID = filename
		fileNode.Type = NodeTypeFile
		fileNode.Name = filename
		fileNode.FilePath = filename
		fileNode.PkgPath = pkg.PkgPath

		mu.Lock()
		result.Nodes = append(result.Nodes, fileNode)

		edge := NewEdge()
		edge.From = pkg.PkgPath
		edge.To = filename
		edge.Type = EdgeTypeContains
		result.Edges = append(result.Edges, edge)

		for _, imp := range file.Imports {
			if imp.Path != nil {
				importPath := strings.Trim(imp.Path.Value, `"`)
				impEdge := NewEdge()
				impEdge.From = filename
				impEdge.To = importPath
				impEdge.Type = EdgeTypeImports
				result.Edges = append(result.Edges, impEdge)
			}
		}
		mu.Unlock()

		var currentFunc string

		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				return true
			}

			switch node := n.(type) {
			case *ast.TypeSpec:
				obj := pkg.TypesInfo.Defs[node.Name]
				if obj != nil {
					var pkgPath string
					if obj.Pkg() != nil {
						pkgPath = obj.Pkg().Path()
					}
					typeID := pkgPath + "." + obj.Name()

					tNode := NewNode()
					tNode.ID = typeID
					tNode.Name = obj.Name()
					tNode.PkgPath = pkgPath
					tNode.FilePath = filename
					start := pkg.Fset.Position(node.Pos()).Line
					end := pkg.Fset.Position(node.End()).Line
					tNode.Lines = [2]int{start, end}

					if _, ok := node.Type.(*ast.StructType); ok {
						tNode.Type = NodeTypeStruct
					} else if _, ok := node.Type.(*ast.InterfaceType); ok {
						tNode.Type = NodeTypeInterface
					} else {
						ReleaseNode(tNode)
						return true
					}

					mu.Lock()
					result.Nodes = append(result.Nodes, tNode)
					
					containsEdge := NewEdge()
					containsEdge.From = filename
					containsEdge.To = typeID
					containsEdge.Type = EdgeTypeContains
					result.Edges = append(result.Edges, containsEdge)
					mu.Unlock()
				}

			case *ast.FuncDecl:
				obj := pkg.TypesInfo.Defs[node.Name]
				if obj != nil {
					var pkgPath string
					if obj.Pkg() != nil {
						pkgPath = obj.Pkg().Path()
					}
					funcID := pkgPath + "." + obj.Name()
					if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
						// It's a method
						funcID = pkgPath + "." + sig.Recv().Type().String() + "." + obj.Name()
					}

					fnNode := NewNode()
					fnNode.ID = funcID
					fnNode.Type = NodeTypeFunc
					fnNode.Name = obj.Name()
					fnNode.PkgPath = pkg.PkgPath
					fnNode.FilePath = filename
					start := pkg.Fset.Position(node.Pos()).Line
					end := pkg.Fset.Position(node.End()).Line
					fnNode.Lines = [2]int{start, end}

					mu.Lock()
					result.Nodes = append(result.Nodes, fnNode)

					containsEdge := NewEdge()
					containsEdge.From = filename
					containsEdge.To = funcID
					containsEdge.Type = EdgeTypeContains
					result.Edges = append(result.Edges, containsEdge)
					mu.Unlock()

					currentFunc = funcID
				}

			case *ast.CallExpr:
				if currentFunc != "" {
					// Identify the function being called
					var calledObj types.Object
					switch fun := node.Fun.(type) {
					case *ast.Ident:
						calledObj = pkg.TypesInfo.Uses[fun]
					case *ast.SelectorExpr:
						calledObj = pkg.TypesInfo.Uses[fun.Sel]
					}

					if calledObj != nil {
						if _, ok := calledObj.(*types.Func); ok {
							var pkgPath string
							if calledObj.Pkg() != nil {
								pkgPath = calledObj.Pkg().Path()
							}
							calledID := pkgPath + "." + calledObj.Name()

							edge := NewEdge()
							edge.From = currentFunc
							edge.To = calledID
							edge.Type = EdgeTypeCalls

							mu.Lock()
							result.Edges = append(result.Edges, edge)
							mu.Unlock()
						}
					}
				}

			case *ast.GoStmt:
				if currentFunc != "" {
					// Spawns relation
					// Try to find the function being called in the go routine
					var calledObj types.Object
					switch fun := node.Call.Fun.(type) {
					case *ast.Ident:
						calledObj = pkg.TypesInfo.Uses[fun]
					case *ast.SelectorExpr:
						calledObj = pkg.TypesInfo.Uses[fun.Sel]
					}

					calledID := "anonymous_func"
					if calledObj != nil {
						if _, ok := calledObj.(*types.Func); ok {
							var pkgPath string
							if calledObj.Pkg() != nil {
								pkgPath = calledObj.Pkg().Path()
							}
							calledID = pkgPath + "." + calledObj.Name()
						}
					} else {
						// It might be an inline anonymous function, construct a unique ID
						calledID = fmt.Sprintf("%s.anon_%d", currentFunc, pkg.Fset.Position(node.Pos()).Line)
					}

					edge := NewEdge()
					edge.From = currentFunc
					edge.To = calledID
					edge.Type = EdgeTypeSpawns

					mu.Lock()
					result.Edges = append(result.Edges, edge)
					mu.Unlock()
				}
				
			case *ast.SendStmt:
				if currentFunc != "" {
					// SENDS_TO
					edge := NewEdge()
					edge.From = currentFunc
					// To identify the channel properly, we'd look at TypesInfo.Types[node.Chan].Type
					chanType := pkg.TypesInfo.TypeOf(node.Chan)
					edge.To = chanType.String()
					edge.Type = EdgeTypeSendsTo
					
					mu.Lock()
					result.Edges = append(result.Edges, edge)
					mu.Unlock()
				}
				
			case *ast.UnaryExpr:
				if currentFunc != "" && node.Op.String() == "<-" {
					// RECEIVES_FROM
					edge := NewEdge()
					edge.From = currentFunc
					chanType := pkg.TypesInfo.TypeOf(node.X)
					edge.To = chanType.String()
					edge.Type = EdgeTypeReceivesFrom
					
					mu.Lock()
					result.Edges = append(result.Edges, edge)
					mu.Unlock()
				}
			}

			return true
		})
	}
	return nil
}
