package parser

import (
	"go/ast"
	"go/constant"
	"go/types"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

const (
	netHTTPPackagePath = "net/http"
	ginPackagePath     = "github.com/gin-gonic/gin"
)

var ginRouteMethods = map[string]string{
	"GET":     "GET",
	"POST":    "POST",
	"PUT":     "PUT",
	"PATCH":   "PATCH",
	"DELETE":  "DELETE",
	"HEAD":    "HEAD",
	"OPTIONS": "OPTIONS",
	"Any":     "ANY",
}

type routePrefix struct {
	value string
	known bool
}

type routeScope struct {
	ginGroups          map[types.Object]routePrefix
	ambiguousGinGroups map[types.Object]bool
}

type routeRegistration struct {
	method   string
	pattern  string
	handlers []ast.Expr
}

func newRouteScope() *routeScope {
	return &routeScope{
		ginGroups:          make(map[types.Object]routePrefix),
		ambiguousGinGroups: make(map[types.Object]bool),
	}
}

func (s *routeScope) clone() *routeScope {
	if s == nil {
		return nil
	}
	cloned := newRouteScope()
	for obj, prefix := range s.ginGroups {
		cloned.ginGroups[obj] = prefix
	}
	for obj, ambiguous := range s.ambiguousGinGroups {
		cloned.ambiguousGinGroups[obj] = ambiguous
	}
	return cloned
}

func (s *routeScope) applyAssign(pkg *packages.Package, stmt *ast.AssignStmt) {
	if s == nil || pkg == nil || stmt == nil {
		return
	}

	type bindingUpdate struct {
		obj    types.Object
		prefix routePrefix
	}
	updates := make([]bindingUpdate, 0, len(stmt.Lhs))
	for i, lhs := range stmt.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || !isGinType(pkg.TypesInfo.TypeOf(ident), "RouterGroup") {
			continue
		}

		obj := pkg.TypesInfo.ObjectOf(ident)
		if obj == nil {
			continue
		}

		prefix := routePrefix{}
		if !s.ambiguousGinGroups[obj] && len(stmt.Lhs) == len(stmt.Rhs) && i < len(stmt.Rhs) {
			prefix = s.resolveGinGroupPrefix(pkg, stmt.Rhs[i])
		}
		updates = append(updates, bindingUpdate{obj: obj, prefix: prefix})
	}
	for _, update := range updates {
		s.ginGroups[update.obj] = update.prefix
	}
}

func (s *routeScope) markControlFlowAssignments(pkg *packages.Package, node ast.Node) {
	if s == nil || pkg == nil || node == nil {
		return
	}

	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}

		stmt, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range stmt.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || !isGinType(pkg.TypesInfo.TypeOf(ident), "RouterGroup") {
				continue
			}
			obj := pkg.TypesInfo.ObjectOf(ident)
			if obj == nil {
				continue
			}
			if _, tracked := s.ginGroups[obj]; !tracked {
				continue
			}
			s.ginGroups[obj] = routePrefix{}
			s.ambiguousGinGroups[obj] = true
		}
		return true
	})
}

func (s *routeScope) applyDecl(pkg *packages.Package, stmt *ast.DeclStmt) {
	if s == nil || pkg == nil || stmt == nil {
		return
	}

	decl, ok := stmt.Decl.(*ast.GenDecl)
	if !ok {
		return
	}

	for _, spec := range decl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range valueSpec.Names {
			if !isGinType(pkg.TypesInfo.TypeOf(name), "RouterGroup") {
				continue
			}

			obj := pkg.TypesInfo.ObjectOf(name)
			if obj == nil {
				continue
			}

			prefix := routePrefix{}
			if len(valueSpec.Names) == len(valueSpec.Values) && i < len(valueSpec.Values) {
				prefix = s.resolveGinGroupPrefix(pkg, valueSpec.Values[i])
			}
			s.ginGroups[obj] = prefix
		}
	}
}

func (s *routeScope) resolveGinGroupPrefix(pkg *packages.Package, expr ast.Expr) routePrefix {
	if s == nil || pkg == nil || expr == nil {
		return routePrefix{}
	}

	switch node := expr.(type) {
	case *ast.ParenExpr:
		return s.resolveGinGroupPrefix(pkg, node.X)
	case *ast.Ident:
		obj := pkg.TypesInfo.ObjectOf(node)
		if obj == nil {
			return routePrefix{}
		}
		return s.ginGroups[obj]
	case *ast.CallExpr:
		selector, ok := node.Fun.(*ast.SelectorExpr)
		if !ok || len(node.Args) == 0 {
			return routePrefix{}
		}
		selection := pkg.TypesInfo.Selections[selector]
		if !isGinSelection(selection, "Group") {
			return routePrefix{}
		}

		groupPath, ok := constantString(pkg, node.Args[0])
		if !ok {
			return routePrefix{}
		}

		var base routePrefix
		switch {
		case isGinType(selection.Recv(), "Engine"):
			base = routePrefix{value: "/", known: true}
		case isGinType(selection.Recv(), "RouterGroup"):
			base = s.resolveGinGroupPrefix(pkg, selector.X)
		default:
			return routePrefix{}
		}
		if !base.known {
			return routePrefix{}
		}
		return routePrefix{value: joinGinPaths(base.value, groupPath), known: true}
	}

	return routePrefix{}
}

func (p *Parser) addRouteEdges(
	pkg *packages.Package,
	ownerID string,
	filename string,
	call *ast.CallExpr,
	createdBoundaryNodes map[string]bool,
	createdRoutes map[string]bool,
	scope *routeScope,
	mu *sync.Mutex,
	result *ParseResult,
) {
	if pkg == nil || call == nil || ownerID == "" || createdRoutes == nil || result == nil {
		return
	}

	registration, ok := detectNetHTTPRoute(pkg, call)
	if !ok {
		registration, ok = detectGinRoute(pkg, call, scope)
	}
	if !ok {
		return
	}

	routeID := BuildID(routeSourceID(pkg, filename), "::route:", registration.method, ":", registration.pattern)
	if !createdRoutes[routeID] {
		createdRoutes[routeID] = true
		start := pkg.Fset.Position(call.Pos()).Line
		end := pkg.Fset.Position(call.End()).Line
		appendNode(mu, result, &Node{
			ID:       routeID,
			Type:     NodeTypeRoute,
			Name:     BuildID(registration.method, " ", registration.pattern),
			PkgPath:  packageGraphPath(pkg),
			FilePath: filename,
			Lines:    [2]int{start, end},
			RepoID:   p.RepoID,
		})
		appendEdge(mu, result, &Edge{
			From:   filename,
			To:     routeID,
			Type:   EdgeTypeContains,
			RepoID: p.RepoID,
		})
	}

	occurrence := edgeOccurrence(pkg, call)
	appendEdge(mu, result, &Edge{
		From:        ownerID,
		To:          routeID,
		Type:        EdgeTypeRegistersRoute,
		RepoID:      p.RepoID,
		Occurrences: []EdgeOccurrence{occurrence},
	})

	seenHandlers := make(map[string]bool)
	for _, handler := range registration.handlers {
		obj := calledObjectFromExpr(pkg, handler)
		handlerID := getFuncID(obj)
		if handlerID == "" || seenHandlers[handlerID] {
			continue
		}
		seenHandlers[handlerID] = true
		p.ensureFunctionBoundaryNode(obj, handlerID, createdBoundaryNodes, mu, result)
		appendEdge(mu, result, &Edge{
			From:        routeID,
			To:          handlerID,
			Type:        EdgeTypeReferences,
			RepoID:      p.RepoID,
			Occurrences: []EdgeOccurrence{occurrence},
		})
	}
}

func detectNetHTTPRoute(pkg *packages.Package, call *ast.CallExpr) (routeRegistration, bool) {
	if len(call.Args) < 2 {
		return routeRegistration{}, false
	}

	fn, ok := calledObjectFromCall(pkg, call).(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != netHTTPPackagePath || fn.Name() != "HandleFunc" {
		return routeRegistration{}, false
	}

	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return routeRegistration{}, false
	}
	if sig.Recv() != nil && !isNamedType(sig.Recv().Type(), netHTTPPackagePath, "ServeMux") {
		return routeRegistration{}, false
	}

	rawPattern, ok := constantString(pkg, call.Args[0])
	if !ok {
		return routeRegistration{}, false
	}
	method, pattern, ok := splitHTTPPattern(rawPattern)
	if !ok {
		return routeRegistration{}, false
	}

	return routeRegistration{
		method:   method,
		pattern:  pattern,
		handlers: []ast.Expr{call.Args[1]},
	}, true
}

func detectGinRoute(pkg *packages.Package, call *ast.CallExpr, scope *routeScope) (routeRegistration, bool) {
	if scope == nil || len(call.Args) == 0 {
		return routeRegistration{}, false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return routeRegistration{}, false
	}
	selection := pkg.TypesInfo.Selections[selector]
	if selection == nil {
		return routeRegistration{}, false
	}

	method, ok := ginRouteMethods[selection.Obj().Name()]
	if !ok || !isGinSelection(selection, selection.Obj().Name()) {
		return routeRegistration{}, false
	}

	relativePath, ok := constantString(pkg, call.Args[0])
	if !ok {
		return routeRegistration{}, false
	}

	var prefix routePrefix
	switch {
	case isGinType(selection.Recv(), "Engine"):
		prefix = routePrefix{value: "/", known: true}
	case isGinType(selection.Recv(), "RouterGroup"):
		prefix = scope.resolveGinGroupPrefix(pkg, selector.X)
	default:
		return routeRegistration{}, false
	}
	if !prefix.known {
		return routeRegistration{}, false
	}

	return routeRegistration{
		method:   method,
		pattern:  joinGinPaths(prefix.value, relativePath),
		handlers: call.Args[1:],
	}, true
}

func isGinSelection(selection *types.Selection, methodName string) bool {
	if selection == nil || selection.Obj() == nil || selection.Obj().Name() != methodName {
		return false
	}
	fn, ok := selection.Obj().(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == ginPackagePath
}

func isGinType(typ types.Type, typeName string) bool {
	return isNamedType(typ, ginPackagePath, typeName)
}

func isNamedType(typ types.Type, pkgPath, typeName string) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	for {
		ptr, ok := typ.(*types.Pointer)
		if !ok {
			break
		}
		typ = types.Unalias(ptr.Elem())
	}
	named, ok := typ.(*types.Named)
	return ok &&
		named.Obj() != nil &&
		named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == pkgPath &&
		named.Obj().Name() == typeName
}

func constantString(pkg *packages.Package, expr ast.Expr) (string, bool) {
	if pkg == nil || pkg.TypesInfo == nil || expr == nil {
		return "", false
	}
	value := pkg.TypesInfo.Types[expr].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

func splitHTTPPattern(raw string) (method string, pattern string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}

	fields := strings.Fields(raw)
	if len(fields) >= 2 && !strings.Contains(fields[0], "/") {
		method = strings.ToUpper(fields[0])
		pattern = strings.TrimSpace(raw[len(fields[0]):])
		if pattern == "" {
			return "", "", false
		}
		return method, pattern, true
	}
	return "ANY", raw, true
}

func joinGinPaths(basePath, relativePath string) string {
	if relativePath == "" {
		if basePath == "" {
			return "/"
		}
		return basePath
	}

	finalPath := path.Join(basePath, relativePath)
	if !strings.HasPrefix(finalPath, "/") {
		finalPath = "/" + finalPath
	}
	if strings.HasSuffix(relativePath, "/") && finalPath != "/" {
		finalPath += "/"
	}
	return finalPath
}

func routeSourceID(pkg *packages.Package, filename string) string {
	if pkg != nil && pkg.Module != nil && pkg.Module.Path != "" && pkg.Module.Dir != "" {
		rel, err := filepath.Rel(pkg.Module.Dir, filename)
		if err == nil && isInsideWorkspace(rel) {
			return BuildID(strings.TrimSuffix(pkg.Module.Path, "/"), "/", filepath.ToSlash(rel))
		}
	}
	return filepath.ToSlash(filename)
}

func calledObjectFromExpr(pkg *packages.Package, expr ast.Expr) types.Object {
	if pkg == nil || pkg.TypesInfo == nil {
		return nil
	}

	switch node := expr.(type) {
	case *ast.ParenExpr:
		return calledObjectFromExpr(pkg, node.X)
	case *ast.Ident:
		return pkg.TypesInfo.Uses[node]
	case *ast.SelectorExpr:
		if selection := pkg.TypesInfo.Selections[node]; selection != nil {
			return selection.Obj()
		}
		return pkg.TypesInfo.Uses[node.Sel]
	case *ast.IndexExpr:
		return calledObjectFromExpr(pkg, node.X)
	case *ast.IndexListExpr:
		return calledObjectFromExpr(pkg, node.X)
	}
	return nil
}
