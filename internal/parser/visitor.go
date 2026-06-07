package parser

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

func (p *Parser) extractPackageEntities(ctx context.Context, pkg *packages.Package, mu *sync.Mutex, result *ParseResult) error {
	createdChannels := make(map[string]bool)

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
		fileNode.RepoID = p.RepoID

		mu.Lock()
		result.Nodes = append(result.Nodes, fileNode)

		edge := NewEdge()
		edge.From = pkg.PkgPath
		edge.To = filename
		edge.Type = EdgeTypeContains
		edge.RepoID = p.RepoID
		result.Edges = append(result.Edges, edge)

		for _, imp := range file.Imports {
			if imp.Path != nil {
				importPath := strings.Trim(imp.Path.Value, `"`)
				impEdge := NewEdge()
				impEdge.From = filename
				impEdge.To = importPath
				impEdge.Type = EdgeTypeImports
				impEdge.RepoID = p.RepoID
				result.Edges = append(result.Edges, impEdge)
			}
		}
		mu.Unlock()

		var currentFunc string
		goCalls := make(map[*ast.CallExpr]bool)

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
					typeID := BuildID(pkgPath, ".", obj.Name())

					tNode := NewNode()
					tNode.ID = typeID
					tNode.Name = obj.Name()
					tNode.PkgPath = pkgPath
					tNode.FilePath = filename
					start := pkg.Fset.Position(node.Pos()).Line
					end := pkg.Fset.Position(node.End()).Line
					tNode.Lines = [2]int{start, end}
					tNode.RepoID = p.RepoID

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
					containsEdge.RepoID = p.RepoID
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
					funcID := BuildID(pkgPath, ".", obj.Name())
					if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
						// It's a method
						funcID = BuildID(pkgPath, ".", sig.Recv().Type().String(), ".", obj.Name())
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
					fnNode.RepoID = p.RepoID

					mu.Lock()
					result.Nodes = append(result.Nodes, fnNode)

					containsEdge := NewEdge()
					containsEdge.From = filename
					containsEdge.To = funcID
					containsEdge.Type = EdgeTypeContains
					containsEdge.RepoID = p.RepoID
					result.Edges = append(result.Edges, containsEdge)
					mu.Unlock()

					currentFunc = funcID
				}

			case *ast.CallExpr:
				if currentFunc != "" && !goCalls[node] {
					// Identify the function being called
					var calledObj types.Object
					switch fun := node.Fun.(type) {
					case *ast.Ident:
						calledObj = pkg.TypesInfo.Uses[fun]
					case *ast.SelectorExpr:
						calledObj = pkg.TypesInfo.Uses[fun.Sel]
					}

					if _, ok := calledObj.(*types.Func); ok {
						var pkgPath string
						if calledObj.Pkg() != nil {
							pkgPath = calledObj.Pkg().Path()
						}
						calledID := BuildID(pkgPath, ".", calledObj.Name())

						edge := NewEdge()
						edge.From = currentFunc
						edge.To = calledID
						edge.Type = EdgeTypeCalls
						edge.RepoID = p.RepoID

						mu.Lock()
						result.Edges = append(result.Edges, edge)
						mu.Unlock()
					}
				}

			case *ast.GoStmt:
				if currentFunc != "" {
					line := pkg.Fset.Position(node.Pos()).Line
					goroutineID := fmt.Sprintf("%s.goroutine_L%d", currentFunc, line)
					goCalls[node.Call] = true

					// Create GOROUTINE node
					grNode := NewNode()
					grNode.ID = goroutineID
					grNode.Type = NodeTypeGoroutine
					grNode.Name = fmt.Sprintf("goroutine_L%d", line)
					grNode.PkgPath = pkg.PkgPath
					grNode.FilePath = filename
					grNode.Lines = [2]int{line, line}
					grNode.RepoID = p.RepoID

					// Determine what function the goroutine calls
					var calledObj types.Object
					switch fun := node.Call.Fun.(type) {
					case *ast.Ident:
						calledObj = pkg.TypesInfo.Uses[fun]
					case *ast.SelectorExpr:
						calledObj = pkg.TypesInfo.Uses[fun.Sel]
					}

					mu.Lock()
					result.Nodes = append(result.Nodes, grNode)

					// currentFunc --SPAWNS--> goroutineNode
					spawnEdge := NewEdge()
					spawnEdge.From = currentFunc
					spawnEdge.To = goroutineID
					spawnEdge.Type = EdgeTypeSpawns
					spawnEdge.RepoID = p.RepoID
					result.Edges = append(result.Edges, spawnEdge)

					// goroutineNode --CALLS--> targetFunc
					if _, ok := calledObj.(*types.Func); ok {
						var pkgPath string
						if calledObj.Pkg() != nil {
							pkgPath = calledObj.Pkg().Path()
						}
						calledID := BuildID(pkgPath, ".", calledObj.Name())

						callEdge := NewEdge()
						callEdge.From = goroutineID
						callEdge.To = calledID
						callEdge.Type = EdgeTypeCalls
						callEdge.RepoID = p.RepoID
						result.Edges = append(result.Edges, callEdge)
					}
					mu.Unlock()
				}

			case *ast.SendStmt:
				if currentFunc != "" {
					chanNodeID := p.resolveChannelNode(pkg, node.Chan, currentFunc, filename, createdChannels, mu, result)
					if chanNodeID != "" {
						sendEdge := NewEdge()
						sendEdge.From = currentFunc
						sendEdge.To = chanNodeID
						sendEdge.Type = EdgeTypeSendsTo
						sendEdge.RepoID = p.RepoID

						mu.Lock()
						result.Edges = append(result.Edges, sendEdge)
						mu.Unlock()
					}
				}

			case *ast.UnaryExpr:
				if currentFunc != "" && node.Op == token.ARROW {
					chanNodeID := p.resolveChannelNode(pkg, node.X, currentFunc, filename, createdChannels, mu, result)
					if chanNodeID != "" {
						recvEdge := NewEdge()
						recvEdge.From = currentFunc
						recvEdge.To = chanNodeID
						recvEdge.Type = EdgeTypeReceivesFrom
						recvEdge.RepoID = p.RepoID

						mu.Lock()
						result.Edges = append(result.Edges, recvEdge)
						mu.Unlock()
					}
				}
			}

			return true
		})
	}
	return nil
}

// resolveChannelNode extracts or creates a CHANNEL node from a channel expression.
func (p *Parser) resolveChannelNode(
	pkg *packages.Package,
	chanExpr ast.Expr,
	currentFunc string,
	filename string,
	created map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) string {
	// Try to resolve the channel variable via TypesInfo
	var chanName string
	var chanObj types.Object

	switch expr := chanExpr.(type) {
	case *ast.Ident:
		chanObj = pkg.TypesInfo.ObjectOf(expr)
		if chanObj != nil {
			chanName = chanObj.Name()
		} else {
			chanName = expr.Name
		}
	case *ast.SelectorExpr:
		chanObj = pkg.TypesInfo.Uses[expr.Sel]
		if chanObj != nil {
			chanName = chanObj.Name()
		} else {
			chanName = expr.Sel.Name
		}
	case *ast.ParenExpr:
		return p.resolveChannelNode(pkg, expr.X, currentFunc, filename, created, mu, result)
	}

	if chanName == "" {
		return ""
	}

	chanType := pkg.TypesInfo.TypeOf(chanExpr)
	if chanType == nil {
		return ""
	}
	if _, ok := chanType.Underlying().(*types.Chan); !ok {
		return ""
	}

	chanID := BuildID(currentFunc, ".", chanName)

	// Determine the channel type string for the display name
	chanTypeStr := chanType.String()

	// Only create the node once
	if !created[chanID] {
		created[chanID] = true

		chNode := NewNode()
		chNode.ID = chanID
		chNode.Type = NodeTypeChannel
		chNode.Name = fmt.Sprintf("%s (%s)", chanName, chanTypeStr)
		chNode.PkgPath = pkg.PkgPath
		chNode.FilePath = filename
		chNode.RepoID = p.RepoID

		mu.Lock()
		result.Nodes = append(result.Nodes, chNode)
		mu.Unlock()
	}

	return chanID
}
