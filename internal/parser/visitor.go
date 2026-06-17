package parser

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

func (p *Parser) extractPackageEntities(ctx context.Context, pkg *packages.Package, mu *sync.Mutex, result *ParseResult) error {
	createdChannels := make(map[string]bool)
	createdBoundaryNodes := make(map[string]bool)

	for _, file := range pkg.Syntax {
		if err := ctx.Err(); err != nil {
			return err
		}

		pos := pkg.Fset.Position(file.Pos())
		filename := pos.Filename

		appendNode(mu, result, &Node{
			ID:       filename,
			Type:     NodeTypeFile,
			Name:     filepath.Base(filename),
			FilePath: filename,
			PkgPath:  pkg.PkgPath,
			RepoID:   p.RepoID,
		})
		appendEdge(mu, result, &Edge{
			From:   pkg.PkgPath,
			To:     filename,
			Type:   EdgeTypeContains,
			RepoID: p.RepoID,
		})

		for _, imp := range file.Imports {
			if imp.Path == nil {
				continue
			}
			importPath := strings.Trim(imp.Path.Value, `"`)
			p.ensureImportBoundaryNode(importPath, createdBoundaryNodes, mu, result)
			appendEdge(mu, result, &Edge{
				From:   filename,
				To:     importPath,
				Type:   EdgeTypeImports,
				RepoID: p.RepoID,
			})
		}

		for _, decl := range file.Decls {
			if err := ctx.Err(); err != nil {
				return err
			}

			switch node := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range node.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok {
						p.extractTypeSpec(pkg, filename, typeSpec, mu, result)
					}
				}
			case *ast.FuncDecl:
				p.extractFuncDecl(pkg, filename, node, createdChannels, createdBoundaryNodes, mu, result)
			}
		}
	}
	return nil
}

func (p *Parser) extractTypeSpec(pkg *packages.Package, filename string, node *ast.TypeSpec, mu *sync.Mutex, result *ParseResult) {
	obj := pkg.TypesInfo.Defs[node.Name]
	if obj == nil {
		return
	}

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
	tNode.Lines = [2]int{
		pkg.Fset.Position(node.Pos()).Line,
		pkg.Fset.Position(node.End()).Line,
	}
	tNode.RepoID = p.RepoID

	if _, ok := node.Type.(*ast.StructType); ok {
		tNode.Type = NodeTypeStruct
	} else if _, ok := node.Type.(*ast.InterfaceType); ok {
		tNode.Type = NodeTypeInterface
	} else {
		ReleaseNode(tNode)
		return
	}

	appendNode(mu, result, tNode)
	appendEdge(mu, result, &Edge{
		From:   filename,
		To:     typeID,
		Type:   EdgeTypeContains,
		RepoID: p.RepoID,
	})
}

func (p *Parser) extractFuncDecl(
	pkg *packages.Package,
	filename string,
	node *ast.FuncDecl,
	createdChannels map[string]bool,
	createdBoundaryNodes map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	obj := pkg.TypesInfo.Defs[node.Name]
	fn, ok := obj.(*types.Func)
	if !ok {
		return
	}

	funcID := getFuncID(fn)
	if funcID == "" {
		return
	}

	sig, _ := fn.Type().(*types.Signature)
	nodeType := NodeTypeFunc
	if sig != nil && sig.Recv() != nil {
		nodeType = NodeTypeMethod
	}

	fnNode := NewNode()
	fnNode.ID = funcID
	fnNode.Type = nodeType
	fnNode.Name = obj.Name()
	fnNode.PkgPath = pkg.PkgPath
	fnNode.FilePath = filename
	fnNode.Lines = [2]int{
		pkg.Fset.Position(node.Pos()).Line,
		pkg.Fset.Position(node.End()).Line,
	}
	fnNode.RepoID = p.RepoID

	appendNode(mu, result, fnNode)
	appendEdge(mu, result, &Edge{
		From:   filename,
		To:     funcID,
		Type:   EdgeTypeContains,
		RepoID: p.RepoID,
	})

	if receiverID, ok := receiverContainerID(sig); ok {
		appendEdge(mu, result, &Edge{
			From:   receiverID,
			To:     funcID,
			Type:   EdgeTypeContains,
			RepoID: p.RepoID,
		})
	}

	p.addTypeReferenceEdges(funcID, sig, mu, result)
	if node.Body != nil {
		p.inspectFunctionBody(pkg, node.Body, funcID, filename, createdChannels, createdBoundaryNodes, mu, result)
	}
}

func (p *Parser) inspectFunctionBody(
	pkg *packages.Package,
	body *ast.BlockStmt,
	currentFunc string,
	filename string,
	createdChannels map[string]bool,
	createdBoundaryNodes map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	goCalls := make(map[*ast.CallExpr]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.GoStmt:
			p.addGoroutineEdges(pkg, node, currentFunc, filename, createdChannels, createdBoundaryNodes, goCalls, mu, result)
		case *ast.CallExpr:
			if !goCalls[node] {
				p.addCallEdge(pkg, currentFunc, node, createdBoundaryNodes, mu, result)
			}
		case *ast.CompositeLit:
			typ := pkg.TypesInfo.TypeOf(node)
			p.addTypeReferenceEdges(currentFunc, typ, mu, result)
			p.addInstantiationEdge(currentFunc, typ, mu, result)
		case *ast.SendStmt:
			chanNodeID := p.resolveChannelNode(pkg, node.Chan, currentFunc, filename, createdChannels, mu, result)
			if chanNodeID != "" {
				appendEdge(mu, result, &Edge{
					From:   currentFunc,
					To:     chanNodeID,
					Type:   EdgeTypeSendsTo,
					RepoID: p.RepoID,
				})
			}
		case *ast.UnaryExpr:
			if node.Op == token.ARROW {
				chanNodeID := p.resolveChannelNode(pkg, node.X, currentFunc, filename, createdChannels, mu, result)
				if chanNodeID != "" {
					appendEdge(mu, result, &Edge{
						From:   currentFunc,
						To:     chanNodeID,
						Type:   EdgeTypeReceivesFrom,
						RepoID: p.RepoID,
					})
				}
			}
		}

		return true
	})
}

func (p *Parser) addGoroutineEdges(
	pkg *packages.Package,
	node *ast.GoStmt,
	currentFunc string,
	filename string,
	createdChannels map[string]bool,
	createdBoundaryNodes map[string]bool,
	goCalls map[*ast.CallExpr]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	if node.Call == nil {
		return
	}

	line := pkg.Fset.Position(node.Pos()).Line
	goroutineID := fmt.Sprintf("%s.goroutine_L%d", currentFunc, line)
	goCalls[node.Call] = true

	appendNode(mu, result, &Node{
		ID:       goroutineID,
		Type:     NodeTypeGoroutine,
		Name:     fmt.Sprintf("goroutine_L%d", line),
		PkgPath:  pkg.PkgPath,
		FilePath: filename,
		Lines:    [2]int{line, line},
		RepoID:   p.RepoID,
	})
	appendEdge(mu, result, &Edge{
		From:   currentFunc,
		To:     goroutineID,
		Type:   EdgeTypeSpawns,
		RepoID: p.RepoID,
	})

	calledObj := calledObjectFromCall(pkg, node.Call)
	if calledID := getFuncID(calledObj); calledID != "" {
		p.ensureFunctionBoundaryNode(calledObj, calledID, createdBoundaryNodes, mu, result)
		appendEdge(mu, result, &Edge{
			From:   goroutineID,
			To:     calledID,
			Type:   EdgeTypeCalls,
			RepoID: p.RepoID,
		})
	}

	if fun, ok := node.Call.Fun.(*ast.FuncLit); ok && fun.Body != nil {
		p.inspectFunctionBody(pkg, fun.Body, goroutineID, filename, createdChannels, createdBoundaryNodes, mu, result)
	}
}

func (p *Parser) addCallEdge(
	pkg *packages.Package,
	currentFunc string,
	node *ast.CallExpr,
	createdBoundaryNodes map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	calledObj := calledObjectFromCall(pkg, node)
	calledID := getFuncID(calledObj)
	if calledID == "" {
		return
	}

	p.ensureFunctionBoundaryNode(calledObj, calledID, createdBoundaryNodes, mu, result)
	appendEdge(mu, result, &Edge{
		From:   currentFunc,
		To:     calledID,
		Type:   EdgeTypeCalls,
		RepoID: p.RepoID,
	})
}

func (p *Parser) addInstantiationEdge(from string, typ types.Type, mu *sync.Mutex, result *ParseResult) {
	typeID, pkgPath, ok := graphTypeID(typ)
	if !ok || !isInternalPackage(pkgPath, p.ModulePrefix) {
		return
	}

	appendEdge(mu, result, &Edge{
		From:   from,
		To:     typeID,
		Type:   EdgeTypeInstantiates,
		RepoID: p.RepoID,
	})
}

func (p *Parser) addTypeReferenceEdges(from string, typ types.Type, mu *sync.Mutex, result *ParseResult) {
	typeIDs := make(map[string]string)
	collectGraphTypeIDs(typ, typeIDs)

	for typeID, pkgPath := range typeIDs {
		if !isInternalPackage(pkgPath, p.ModulePrefix) {
			continue
		}
		appendEdge(mu, result, &Edge{
			From:   from,
			To:     typeID,
			Type:   EdgeTypeReferences,
			RepoID: p.RepoID,
		})
	}
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
	if !created[chanID] {
		created[chanID] = true

		appendNode(mu, result, &Node{
			ID:       chanID,
			Type:     NodeTypeChannel,
			Name:     fmt.Sprintf("%s (%s)", chanName, chanType.String()),
			PkgPath:  pkg.PkgPath,
			FilePath: filename,
			RepoID:   p.RepoID,
		})
	}

	return chanID
}

func (p *Parser) ensureImportBoundaryNode(importPath string, created map[string]bool, mu *sync.Mutex, result *ParseResult) {
	if importPath == "" || isInternalPackage(importPath, p.ModulePrefix) {
		return
	}
	p.ensureBoundaryNode(importPath, boundaryName(importPath), importPath, created, mu, result)
}

func (p *Parser) ensureFunctionBoundaryNode(obj types.Object, id string, created map[string]bool, mu *sync.Mutex, result *ParseResult) {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return
	}

	pkgPath := fn.Pkg().Path()
	if isInternalPackage(pkgPath, p.ModulePrefix) {
		return
	}
	p.ensureBoundaryNode(id, fn.Name(), pkgPath, created, mu, result)
}

func (p *Parser) ensureBoundaryNode(id, name, pkgPath string, created map[string]bool, mu *sync.Mutex, result *ParseResult) {
	if id == "" || created[id] {
		return
	}
	created[id] = true

	appendNode(mu, result, &Node{
		ID:      id,
		Type:    NodeTypeBoundary,
		Name:    name,
		PkgPath: pkgPath,
		RepoID:  p.RepoID,
	})
}

func appendNode(mu *sync.Mutex, result *ParseResult, node *Node) {
	mu.Lock()
	result.Nodes = append(result.Nodes, node)
	mu.Unlock()
}

func appendEdge(mu *sync.Mutex, result *ParseResult, edge *Edge) {
	mu.Lock()
	result.Edges = append(result.Edges, edge)
	mu.Unlock()
}

func calledObjectFromCall(pkg *packages.Package, call *ast.CallExpr) types.Object {
	if call == nil {
		return nil
	}

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return pkg.TypesInfo.Uses[fun]
	case *ast.SelectorExpr:
		if selection := pkg.TypesInfo.Selections[fun]; selection != nil {
			return selection.Obj()
		}
		return pkg.TypesInfo.Uses[fun.Sel]
	}

	return nil
}

// getFuncID returns the unique identifier for a function or method.
func getFuncID(calledObj types.Object) string {
	fn, ok := calledObj.(*types.Func)
	if !ok {
		return ""
	}

	var pkgPath string
	if fn.Pkg() != nil {
		pkgPath = fn.Pkg().Path()
	}
	funcID := BuildID(pkgPath, ".", fn.Name())
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		funcID = BuildID(pkgPath, ".", sig.Recv().Type().String(), ".", fn.Name())
	}
	return funcID
}

func receiverContainerID(sig *types.Signature) (string, bool) {
	if sig == nil || sig.Recv() == nil {
		return "", false
	}
	typeID, _, ok := graphTypeID(sig.Recv().Type())
	return typeID, ok
}

func graphTypeID(typ types.Type) (string, string, bool) {
	for {
		ptr, ok := typ.(*types.Pointer)
		if !ok {
			break
		}
		typ = ptr.Elem()
	}

	named, ok := typ.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", "", false
	}

	switch named.Underlying().(type) {
	case *types.Struct, *types.Interface:
		pkgPath := named.Obj().Pkg().Path()
		return BuildID(pkgPath, ".", named.Obj().Name()), pkgPath, true
	default:
		return "", "", false
	}
}

func collectGraphTypeIDs(typ types.Type, out map[string]string) {
	if typ == nil {
		return
	}

	if typeID, pkgPath, ok := graphTypeID(typ); ok {
		out[typeID] = pkgPath
	}

	switch t := typ.(type) {
	case *types.Pointer:
		collectGraphTypeIDs(t.Elem(), out)
	case *types.Slice:
		collectGraphTypeIDs(t.Elem(), out)
	case *types.Array:
		collectGraphTypeIDs(t.Elem(), out)
	case *types.Chan:
		collectGraphTypeIDs(t.Elem(), out)
	case *types.Map:
		collectGraphTypeIDs(t.Key(), out)
		collectGraphTypeIDs(t.Elem(), out)
	case *types.Signature:
		if t.Recv() != nil {
			collectGraphTypeIDs(t.Recv().Type(), out)
		}
		collectTupleGraphTypeIDs(t.Params(), out)
		collectTupleGraphTypeIDs(t.Results(), out)
	case *types.Tuple:
		collectTupleGraphTypeIDs(t, out)
	}
}

func collectTupleGraphTypeIDs(tuple *types.Tuple, out map[string]string) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		collectGraphTypeIDs(tuple.At(i).Type(), out)
	}
}

func boundaryName(id string) string {
	if idx := strings.LastIndex(id, "/"); idx >= 0 && idx < len(id)-1 {
		return id[idx+1:]
	}
	return id
}
