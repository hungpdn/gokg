package parser

import (
	"go/ast"
	"go/constant"
	"go/token"
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
	value    string
	known    bool
	handlers []ast.Expr
}

type staticString struct {
	value string
	known bool
}

type routeScope struct {
	ginGroups          map[types.Object]routePrefix
	ambiguousGinGroups map[types.Object]bool
	staticStrings      map[types.Object]staticString
	ambiguousStrings   map[types.Object]bool
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
		staticStrings:      make(map[types.Object]staticString),
		ambiguousStrings:   make(map[types.Object]bool),
	}
}

func (s *routeScope) clone() *routeScope {
	if s == nil {
		return nil
	}
	cloned := newRouteScope()
	for obj, prefix := range s.ginGroups {
		if len(prefix.handlers) > 0 {
			prefix.handlers = append([]ast.Expr(nil), prefix.handlers...)
		}
		cloned.ginGroups[obj] = prefix
	}
	for obj, ambiguous := range s.ambiguousGinGroups {
		cloned.ambiguousGinGroups[obj] = ambiguous
	}
	for obj, value := range s.staticStrings {
		cloned.staticStrings[obj] = value
	}
	for obj, ambiguous := range s.ambiguousStrings {
		cloned.ambiguousStrings[obj] = ambiguous
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

	s.applyStringAssign(pkg, stmt)
}

func (s *routeScope) applyStringAssign(pkg *packages.Package, stmt *ast.AssignStmt) {
	type bindingUpdate struct {
		obj   types.Object
		value staticString
	}

	updates := make([]bindingUpdate, 0, len(stmt.Lhs))
	for i, lhs := range stmt.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || !isStringType(pkg.TypesInfo.TypeOf(ident)) {
			continue
		}

		obj := pkg.TypesInfo.ObjectOf(ident)
		if obj == nil {
			continue
		}

		value := staticString{}
		if !s.ambiguousStrings[obj] && len(stmt.Lhs) == len(stmt.Rhs) && i < len(stmt.Rhs) {
			if resolved, ok := s.resolveStaticString(pkg, stmt.Rhs[i]); ok {
				value = staticString{value: resolved, known: true}
			}
		}
		updates = append(updates, bindingUpdate{obj: obj, value: value})
	}
	for _, update := range updates {
		s.staticStrings[update.obj] = update.value
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
			if !ok {
				continue
			}
			obj := pkg.TypesInfo.ObjectOf(ident)
			if obj == nil {
				continue
			}

			switch {
			case isGinType(pkg.TypesInfo.TypeOf(ident), "RouterGroup"):
				if _, tracked := s.ginGroups[obj]; !tracked {
					continue
				}
				s.ginGroups[obj] = routePrefix{}
				s.ambiguousGinGroups[obj] = true
			case isStringType(pkg.TypesInfo.TypeOf(ident)):
				if _, tracked := s.staticStrings[obj]; !tracked {
					continue
				}
				s.staticStrings[obj] = staticString{}
				s.ambiguousStrings[obj] = true
			}
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
			switch {
			case isGinType(pkg.TypesInfo.TypeOf(name), "RouterGroup"):
				obj := pkg.TypesInfo.ObjectOf(name)
				if obj == nil {
					continue
				}

				prefix := routePrefix{}
				if len(valueSpec.Names) == len(valueSpec.Values) && i < len(valueSpec.Values) {
					prefix = s.resolveGinGroupPrefix(pkg, valueSpec.Values[i])
				}
				s.ginGroups[obj] = prefix
			case isStringType(pkg.TypesInfo.TypeOf(name)):
				obj := pkg.TypesInfo.ObjectOf(name)
				if obj == nil {
					continue
				}

				value := staticString{}
				if len(valueSpec.Names) == len(valueSpec.Values) && i < len(valueSpec.Values) {
					if resolved, ok := s.resolveStaticString(pkg, valueSpec.Values[i]); ok {
						value = staticString{value: resolved, known: true}
					}
				}
				s.staticStrings[obj] = value
			default:
				continue
			}
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

		groupPath, ok := s.resolveStaticString(pkg, node.Args[0])
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
		return routePrefix{
			value:    joinGinPaths(base.value, groupPath),
			known:    true,
			handlers: appendRouteHandlers(base.handlers, node.Args[1:]...),
		}
	}

	return routePrefix{}
}

func (s *routeScope) resolveStaticString(pkg *packages.Package, expr ast.Expr) (string, bool) {
	if value, ok := constantString(pkg, expr); ok {
		return value, true
	}
	if s == nil || pkg == nil || expr == nil {
		return "", false
	}

	switch node := expr.(type) {
	case *ast.ParenExpr:
		return s.resolveStaticString(pkg, node.X)
	case *ast.Ident:
		obj := pkg.TypesInfo.ObjectOf(node)
		if obj == nil {
			return "", false
		}
		value := s.staticStrings[obj]
		return value.value, value.known
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, ok := s.resolveStaticString(pkg, node.X)
		if !ok {
			return "", false
		}
		right, ok := s.resolveStaticString(pkg, node.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
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

	registrations := detectNetHTTPRoutes(pkg, call, scope)
	if len(registrations) == 0 {
		registrations = detectGinRoutes(pkg, call, scope)
	}
	if len(registrations) == 0 {
		return
	}

	occurrence := edgeOccurrence(pkg, call)
	for _, registration := range registrations {
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
}

func detectNetHTTPRoutes(pkg *packages.Package, call *ast.CallExpr, scope *routeScope) []routeRegistration {
	if len(call.Args) < 2 {
		return nil
	}

	fn, ok := calledObjectFromCall(pkg, call).(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != netHTTPPackagePath || !isNetHTTPRouteFunction(fn.Name()) {
		return nil
	}

	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil
	}
	if sig.Recv() != nil && !isNamedType(sig.Recv().Type(), netHTTPPackagePath, "ServeMux") {
		return nil
	}

	rawPattern, ok := routeStaticString(pkg, scope, call.Args[0])
	if !ok {
		return nil
	}
	method, pattern, ok := splitHTTPPattern(rawPattern)
	if !ok {
		return nil
	}

	return []routeRegistration{{
		method:   method,
		pattern:  pattern,
		handlers: []ast.Expr{call.Args[1]},
	}}
}

func detectGinRoutes(pkg *packages.Package, call *ast.CallExpr, scope *routeScope) []routeRegistration {
	if scope == nil || len(call.Args) == 0 {
		return nil
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	selection := pkg.TypesInfo.Selections[selector]
	if selection == nil || selection.Obj() == nil {
		return nil
	}
	methodName := selection.Obj().Name()
	if !isGinSelection(selection, methodName) {
		return nil
	}

	prefix, ok := scope.resolveGinCallPrefix(pkg, selector, selection)
	if !ok {
		return nil
	}

	switch methodName {
	case "Handle":
		if len(call.Args) < 2 {
			return nil
		}
		method, ok := scope.resolveStaticString(pkg, call.Args[0])
		if !ok {
			return nil
		}
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			return nil
		}
		relativePath, ok := scope.resolveStaticString(pkg, call.Args[1])
		if !ok {
			return nil
		}
		return []routeRegistration{{
			method:   method,
			pattern:  joinGinPaths(prefix.value, relativePath),
			handlers: appendRouteHandlers(prefix.handlers, call.Args[2:]...),
		}}
	case "Static", "StaticFS":
		relativePath, ok := scope.resolveStaticString(pkg, call.Args[0])
		if !ok {
			return nil
		}
		pattern := appendGinStaticWildcard(joinGinPaths(prefix.value, relativePath))
		return []routeRegistration{
			{method: "GET", pattern: pattern, handlers: prefix.handlers},
			{method: "HEAD", pattern: pattern, handlers: prefix.handlers},
		}
	case "StaticFile", "StaticFileFS":
		relativePath, ok := scope.resolveStaticString(pkg, call.Args[0])
		if !ok {
			return nil
		}
		pattern := joinGinPaths(prefix.value, relativePath)
		return []routeRegistration{
			{method: "GET", pattern: pattern, handlers: prefix.handlers},
			{method: "HEAD", pattern: pattern, handlers: prefix.handlers},
		}
	}

	method, ok := ginRouteMethods[methodName]
	if !ok {
		return nil
	}
	relativePath, ok := scope.resolveStaticString(pkg, call.Args[0])
	if !ok {
		return nil
	}

	return []routeRegistration{{
		method:   method,
		pattern:  joinGinPaths(prefix.value, relativePath),
		handlers: appendRouteHandlers(prefix.handlers, call.Args[1:]...),
	}}
}

func isNetHTTPRouteFunction(name string) bool {
	return name == "Handle" || name == "HandleFunc"
}

func routeStaticString(pkg *packages.Package, scope *routeScope, expr ast.Expr) (string, bool) {
	if scope != nil {
		return scope.resolveStaticString(pkg, expr)
	}
	return constantString(pkg, expr)
}

func (s *routeScope) resolveGinCallPrefix(pkg *packages.Package, selector *ast.SelectorExpr, selection *types.Selection) (routePrefix, bool) {
	switch {
	case isGinType(selection.Recv(), "Engine"):
		return routePrefix{value: "/", known: true}, true
	case isGinType(selection.Recv(), "RouterGroup"):
		prefix := s.resolveGinGroupPrefix(pkg, selector.X)
		return prefix, prefix.known
	default:
		return routePrefix{}, false
	}
}

func appendGinStaticWildcard(pattern string) string {
	pattern = strings.TrimRight(pattern, "/")
	if pattern == "" {
		return "/*filepath"
	}
	return pattern + "/*filepath"
}

func appendRouteHandlers(base []ast.Expr, extra ...ast.Expr) []ast.Expr {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	handlers := make([]ast.Expr, 0, len(base)+len(extra))
	handlers = append(handlers, base...)
	handlers = append(handlers, extra...)
	return handlers
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

func isStringType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	return ok && basic.Kind() == types.String
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
	case *ast.CallExpr:
		obj := calledObjectFromCall(pkg, node)
		if _, ok := obj.(*types.TypeName); ok && len(node.Args) == 1 {
			return calledObjectFromExpr(pkg, node.Args[0])
		}
		if _, ok := obj.(*types.Func); ok {
			return obj
		}
	case *ast.IndexExpr:
		return calledObjectFromExpr(pkg, node.X)
	case *ast.IndexListExpr:
		return calledObjectFromExpr(pkg, node.X)
	}
	return nil
}
