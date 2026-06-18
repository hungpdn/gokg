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
	pkgPath := packageGraphPath(pkg)

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
			PkgPath:  pkgPath,
			RepoID:   p.RepoID,
		})
		appendEdge(mu, result, &Edge{
			From:   pkgPath,
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
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						p.extractTypeSpec(pkg, filename, spec, mu, result)
					case *ast.ValueSpec:
						p.extractValueSpec(pkg, filename, node.Tok, spec, createdChannels, createdBoundaryNodes, mu, result)
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
		pkgPath = normalizePackagePath(obj.Pkg().Path())
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
		tNode.Type = NodeTypeTypeAlias
	}

	appendNode(mu, result, tNode)
	appendEdge(mu, result, &Edge{
		From:   filename,
		To:     typeID,
		Type:   EdgeTypeContains,
		RepoID: p.RepoID,
	})
	p.addTypeReferenceEdges(typeID, pkg.TypesInfo.TypeOf(node.Type), mu, result)
	p.addTypeReferenceEdges(typeID, obj.Type(), mu, result)
	p.addTypeReferenceEdges(typeID, obj.Type().Underlying(), mu, result)

	if iface, ok := node.Type.(*ast.InterfaceType); ok {
		p.extractInterfaceMethods(pkg, filename, typeID, iface, mu, result)
	}
}

func (p *Parser) extractInterfaceMethods(
	pkg *packages.Package,
	filename string,
	interfaceID string,
	iface *ast.InterfaceType,
	mu *sync.Mutex,
	result *ParseResult,
) {
	if iface.Methods == nil {
		return
	}

	for _, field := range iface.Methods.List {
		funcType, ok := field.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			obj := pkg.TypesInfo.Defs[name]
			fn, ok := obj.(*types.Func)
			if !ok {
				continue
			}

			var pkgPath string
			if fn.Pkg() != nil {
				pkgPath = normalizePackagePath(fn.Pkg().Path())
			}
			methodID := BuildID(pkgPath, ".", interfaceID, ".", name.Name)

			methodNode := NewNode()
			methodNode.ID = methodID
			methodNode.Type = NodeTypeMethod
			methodNode.Name = name.Name
			methodNode.PkgPath = packageGraphPath(pkg)
			methodNode.FilePath = filename
			methodNode.Lines = [2]int{
				pkg.Fset.Position(field.Pos()).Line,
				pkg.Fset.Position(field.End()).Line,
			}
			methodNode.RepoID = p.RepoID

			appendNode(mu, result, methodNode)
			appendEdge(mu, result, &Edge{
				From:   interfaceID,
				To:     methodID,
				Type:   EdgeTypeContains,
				RepoID: p.RepoID,
			})
			if sig, ok := fn.Type().(*types.Signature); ok {
				p.addTypeReferenceEdges(methodID, sig, mu, result)
			} else {
				p.addTypeReferenceEdges(methodID, pkg.TypesInfo.TypeOf(funcType), mu, result)
			}
		}
	}
}

func (p *Parser) extractValueSpec(
	pkg *packages.Package,
	filename string,
	tok token.Token,
	node *ast.ValueSpec,
	createdChannels map[string]bool,
	createdBoundaryNodes map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	for _, name := range node.Names {
		if name.Name == "_" {
			continue
		}

		obj := pkg.TypesInfo.Defs[name]
		if obj == nil {
			continue
		}
		if !isPackageScopeObject(obj) {
			continue
		}

		var pkgPath string
		if obj.Pkg() != nil {
			pkgPath = normalizePackagePath(obj.Pkg().Path())
		}
		symbolID := BuildID(pkgPath, ".", obj.Name())

		valueNode := NewNode()
		valueNode.ID = symbolID
		valueNode.Type = nodeTypeForValue(tok)
		valueNode.Name = obj.Name()
		valueNode.PkgPath = pkgPath
		valueNode.FilePath = filename
		valueNode.Lines = [2]int{
			pkg.Fset.Position(name.Pos()).Line,
			pkg.Fset.Position(node.End()).Line,
		}
		valueNode.RepoID = p.RepoID

		appendNode(mu, result, valueNode)
		appendEdge(mu, result, &Edge{
			From:   filename,
			To:     symbolID,
			Type:   EdgeTypeContains,
			RepoID: p.RepoID,
		})
		p.addTypeReferenceEdges(symbolID, obj.Type(), mu, result)

		for _, value := range node.Values {
			p.addExpressionReferenceEdges(pkg, symbolID, value, filename, createdChannels, createdBoundaryNodes, mu, result)
		}
	}
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
	fnNode.PkgPath = packageGraphPath(pkg)
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
	goFuncLits := make(map[*ast.FuncLit]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch node := n.(type) {
		case *ast.FuncLit:
			if goFuncLits[node] {
				return false
			}
			if node.Body != nil {
				p.inspectFunctionBody(pkg, node.Body, currentFunc, filename, createdChannels, createdBoundaryNodes, mu, result)
			}
			return false
		case *ast.GoStmt:
			p.addGoroutineEdges(pkg, node, currentFunc, filename, createdChannels, createdBoundaryNodes, goCalls, goFuncLits, mu, result)
		case *ast.CallExpr:
			if !goCalls[node] {
				p.addCallEdge(pkg, currentFunc, node, createdBoundaryNodes, mu, result)
				p.addChannelArgumentFlowEdges(pkg, node, currentFunc, filename, createdChannels, mu, result)
			}
		case *ast.CompositeLit:
			typ := pkg.TypesInfo.TypeOf(node)
			p.addTypeReferenceEdges(currentFunc, typ, mu, result)
			p.addInstantiationEdge(currentFunc, typ, edgeOccurrence(pkg, node), mu, result)
		case *ast.RangeStmt:
			chanNodeID := p.resolveChannelNode(pkg, node.X, currentFunc, filename, createdChannels, mu, result)
			if chanNodeID != "" {
				appendEdge(mu, result, &Edge{
					From:   currentFunc,
					To:     chanNodeID,
					Type:   EdgeTypeReceivesFrom,
					RepoID: p.RepoID,
				})
			}
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

func (p *Parser) addChannelArgumentFlowEdges(
	pkg *packages.Package,
	call *ast.CallExpr,
	currentFunc string,
	filename string,
	createdChannels map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	calledObj := calledObjectFromCall(pkg, call)
	calledID := getFuncID(calledObj)
	if calledID == "" {
		return
	}

	fn, ok := calledObj.(*types.Func)
	if !ok {
		return
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Params() == nil {
		return
	}

	params := sig.Params()
	for argIndex, arg := range call.Args {
		paramIndex := argIndex
		if sig.Variadic() && argIndex >= params.Len()-1 {
			paramIndex = params.Len() - 1
		}
		if paramIndex < 0 || paramIndex >= params.Len() {
			continue
		}

		param := params.At(paramIndex)
		if _, ok := channelType(param.Type()); !ok {
			continue
		}
		if _, ok := channelType(pkg.TypesInfo.TypeOf(arg)); !ok {
			continue
		}

		argChannelID := p.resolveChannelNode(pkg, arg, currentFunc, filename, createdChannels, mu, result)
		if argChannelID == "" {
			continue
		}

		paramChannelID, _ := channelNodeIdentity(pkg, param, calledID, param.Name())
		appendChannelArgFlow(mu, result, channelArgFlow{
			CalleeID:       calledID,
			ParamChannelID: paramChannelID,
			ArgChannelID:   argChannelID,
			RepoID:         p.RepoID,
		})
	}
}

func (p *Parser) resolveChannelArgumentFlowEdges(result *ParseResult) {
	if result == nil || len(result.channelArgFlows) == 0 {
		return
	}

	observed := make(map[string]map[EdgeType]bool)
	existing := make(map[string]bool)
	for _, edge := range result.Edges {
		if edge == nil {
			continue
		}
		existing[edgeIdentity(edge.From, edge.To, edge.Type)] = true
		if edge.Type != EdgeTypeSendsTo && edge.Type != EdgeTypeReceivesFrom {
			continue
		}
		key := channelFlowKey(edge.From, edge.To)
		if observed[key] == nil {
			observed[key] = make(map[EdgeType]bool)
		}
		observed[key][edge.Type] = true
	}

	for _, flow := range result.channelArgFlows {
		for _, edgeType := range []EdgeType{EdgeTypeSendsTo, EdgeTypeReceivesFrom} {
			if !observed[channelFlowKey(flow.CalleeID, flow.ParamChannelID)][edgeType] {
				continue
			}
			key := edgeIdentity(flow.CalleeID, flow.ArgChannelID, edgeType)
			if existing[key] {
				continue
			}
			existing[key] = true
			result.Edges = append(result.Edges, &Edge{
				From:   flow.CalleeID,
				To:     flow.ArgChannelID,
				Type:   edgeType,
				RepoID: flow.RepoID,
			})
		}
	}
}

func channelFlowKey(from, to string) string {
	return BuildID(from, "\x00", to)
}

func edgeIdentity(from string, to string, edgeType EdgeType) string {
	return BuildID(from, "\x00", to, "\x00", string(edgeType))
}

func (p *Parser) addGoroutineEdges(
	pkg *packages.Package,
	node *ast.GoStmt,
	currentFunc string,
	filename string,
	createdChannels map[string]bool,
	createdBoundaryNodes map[string]bool,
	goCalls map[*ast.CallExpr]bool,
	goFuncLits map[*ast.FuncLit]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	if node.Call == nil {
		return
	}

	line := pkg.Fset.Position(node.Pos()).Line
	goroutineID := fmt.Sprintf("%s.goroutine_L%d", currentFunc, line)
	goCalls[node.Call] = true
	if fun, ok := node.Call.Fun.(*ast.FuncLit); ok {
		goFuncLits[fun] = true
	}

	appendNode(mu, result, &Node{
		ID:       goroutineID,
		Type:     NodeTypeGoroutine,
		Name:     fmt.Sprintf("goroutine_L%d", line),
		PkgPath:  packageGraphPath(pkg),
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
			From:        goroutineID,
			To:          calledID,
			Type:        EdgeTypeCalls,
			RepoID:      p.RepoID,
			Occurrences: []EdgeOccurrence{edgeOccurrence(pkg, node.Call)},
		})
	}
	p.addChannelArgumentFlowEdges(pkg, node.Call, currentFunc, filename, createdChannels, mu, result)

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
		From:        currentFunc,
		To:          calledID,
		Type:        EdgeTypeCalls,
		RepoID:      p.RepoID,
		Occurrences: []EdgeOccurrence{edgeOccurrence(pkg, node)},
	})
}

func (p *Parser) addInstantiationEdge(from string, typ types.Type, occurrence EdgeOccurrence, mu *sync.Mutex, result *ParseResult) {
	typeID, pkgPath, ok := graphTypeID(typ)
	if !ok || !isInternalPackage(pkgPath, p.ModulePrefix) {
		return
	}

	edge := &Edge{
		From:   from,
		To:     typeID,
		Type:   EdgeTypeInstantiates,
		RepoID: p.RepoID,
	}
	if occurrence != (EdgeOccurrence{}) {
		edge.Occurrences = []EdgeOccurrence{occurrence}
	}

	appendEdge(mu, result, edge)
}

func (p *Parser) addTypeReferenceEdges(from string, typ types.Type, mu *sync.Mutex, result *ParseResult) {
	typeIDs := make(map[string]string)
	collectGraphTypeIDs(typ, typeIDs)

	for typeID, pkgPath := range typeIDs {
		if typeID == from {
			continue
		}
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

func (p *Parser) addExpressionReferenceEdges(
	pkg *packages.Package,
	from string,
	expr ast.Expr,
	filename string,
	createdChannels map[string]bool,
	createdBoundaryNodes map[string]bool,
	mu *sync.Mutex,
	result *ParseResult,
) {
	seen := make(map[string]bool)

	ast.Inspect(expr, func(n ast.Node) bool {
		if n == nil {
			return true
		}

		switch node := n.(type) {
		case *ast.FuncLit:
			if node.Body != nil {
				p.inspectFunctionBody(pkg, node.Body, from, filename, createdChannels, createdBoundaryNodes, mu, result)
			}
			return false
		case *ast.CallExpr:
			p.addCallEdge(pkg, from, node, createdBoundaryNodes, mu, result)
			p.addChannelArgumentFlowEdges(pkg, node, from, filename, createdChannels, mu, result)
		case *ast.CompositeLit:
			typ := pkg.TypesInfo.TypeOf(node)
			p.addTypeReferenceEdges(from, typ, mu, result)
			p.addInstantiationEdge(from, typ, edgeOccurrence(pkg, node), mu, result)
		case *ast.Ident:
			p.addObjectReferenceEdge(from, pkg.TypesInfo.Uses[node], seen, mu, result)
		case *ast.SelectorExpr:
			if selection := pkg.TypesInfo.Selections[node]; selection != nil {
				p.addObjectReferenceEdge(from, selection.Obj(), seen, mu, result)
			}
			p.addObjectReferenceEdge(from, pkg.TypesInfo.Uses[node.Sel], seen, mu, result)
		}

		return true
	})
}

func (p *Parser) addObjectReferenceEdge(from string, obj types.Object, seen map[string]bool, mu *sync.Mutex, result *ParseResult) {
	id, pkgPath, ok := graphObjectID(obj)
	if !ok || id == from || seen[id] {
		return
	}
	if !isInternalPackage(pkgPath, p.ModulePrefix) {
		return
	}

	seen[id] = true
	appendEdge(mu, result, &Edge{
		From:   from,
		To:     id,
		Type:   EdgeTypeReferences,
		RepoID: p.RepoID,
	})
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
		if selection := pkg.TypesInfo.Selections[expr]; selection != nil {
			chanName = selectorChannelName(expr)
		} else {
			chanObj = pkg.TypesInfo.Uses[expr.Sel]
			if chanObj != nil {
				chanName = chanObj.Name()
			} else {
				chanName = expr.Sel.Name
			}
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

	chanID, lines := channelNodeIdentity(pkg, chanObj, currentFunc, chanName)
	if !created[chanID] {
		created[chanID] = true

		appendNode(mu, result, &Node{
			ID:       chanID,
			Type:     NodeTypeChannel,
			Name:     fmt.Sprintf("%s (%s)", chanName, graphTypeString(chanType)),
			PkgPath:  packageGraphPath(pkg),
			FilePath: filename,
			Lines:    lines,
			RepoID:   p.RepoID,
		})
	}

	return chanID
}

func selectorChannelName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		prefix := selectorChannelName(node.X)
		if prefix == "" {
			return node.Sel.Name
		}
		return BuildID(prefix, ".", node.Sel.Name)
	default:
		return ""
	}
}

func channelType(typ types.Type) (*types.Chan, bool) {
	if typ == nil {
		return nil, false
	}
	chanType, ok := typ.Underlying().(*types.Chan)
	return chanType, ok
}

func channelNodeIdentity(pkg *packages.Package, obj types.Object, currentFunc string, name string) (string, [2]int) {
	if obj == nil || obj.Pos() == token.NoPos {
		return BuildID(currentFunc, ".", name), [2]int{}
	}

	pos := pkg.Fset.Position(obj.Pos())
	if !pos.IsValid() || pos.Filename == "" {
		return BuildID(currentFunc, ".", name), [2]int{}
	}

	pkgPath := packageGraphPath(pkg)
	if obj.Pkg() != nil {
		pkgPath = normalizePackagePath(obj.Pkg().Path())
	}
	id := fmt.Sprintf("channel:%s:%s:%d:%d:%s", pkgPath, filepath.ToSlash(pos.Filename), pos.Line, pos.Column, obj.Name())
	return id, [2]int{pos.Line, pos.Line}
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
	pkgPath = normalizePackagePath(pkgPath)
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

func appendChannelArgFlow(mu *sync.Mutex, result *ParseResult, flow channelArgFlow) {
	mu.Lock()
	result.channelArgFlows = append(result.channelArgFlows, flow)
	mu.Unlock()
}

func edgeOccurrence(pkg *packages.Package, node ast.Node) EdgeOccurrence {
	if pkg == nil || node == nil {
		return EdgeOccurrence{}
	}
	pos := pkg.Fset.Position(node.Pos())
	return EdgeOccurrence{
		FilePath: pos.Filename,
		Line:     pos.Line,
		Column:   pos.Column,
	}
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
		pkgPath = normalizePackagePath(fn.Pkg().Path())
	}
	funcID := BuildID(pkgPath, ".", fn.Name())
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		funcID = BuildID(pkgPath, ".", graphTypeString(sig.Recv().Type()), ".", fn.Name())
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

	pkgPath := normalizePackagePath(named.Obj().Pkg().Path())
	return BuildID(pkgPath, ".", named.Obj().Name()), pkgPath, true
}

func graphObjectID(obj types.Object) (string, string, bool) {
	if obj == nil || !isPackageScopeObject(obj) {
		return "", "", false
	}

	switch obj.(type) {
	case *types.Const, *types.TypeName, *types.Var:
	default:
		return "", "", false
	}

	pkgPath := normalizePackagePath(obj.Pkg().Path())
	return BuildID(pkgPath, ".", obj.Name()), pkgPath, true
}

func isPackageScopeObject(obj types.Object) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Parent() == obj.Pkg().Scope()
}

func graphTypeString(typ types.Type) string {
	if typ == nil {
		return ""
	}
	return types.TypeString(typ, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return normalizePackagePath(pkg.Path())
	})
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

func nodeTypeForValue(tok token.Token) NodeType {
	if tok == token.CONST {
		return NodeTypeConstant
	}
	return NodeTypeVariable
}

func boundaryName(id string) string {
	if idx := strings.LastIndex(id, "/"); idx >= 0 && idx < len(id)-1 {
		return id[idx+1:]
	}
	return id
}
