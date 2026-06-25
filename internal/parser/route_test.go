package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceExtractsNetHTTPRoutes(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/routes\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), `package routes

import "net/http"

const healthPattern = "/healthz"

type fakeMux struct{}

func (fakeMux) HandleFunc(string, func(http.ResponseWriter, *http.Request)) {}
func (fakeMux) Handle(string, http.Handler)                                 {}
func (fakeMux) GET(string, func(http.ResponseWriter, *http.Request))        {}

func handle(http.ResponseWriter, *http.Request) {}

func register(mux *http.ServeMux) {
	mux.HandleFunc(healthPattern, handle)
	mux.Handle("GET /mux-handle", http.HandlerFunc(handle))
	http.HandleFunc("GET /ready", handle)
	http.HandleFunc("GET /same", handle)
	http.HandleFunc("POST /same", handle)
	http.Handle("/plain-handle", http.HandlerFunc(handle))
	localPattern := "/local"
	http.HandleFunc(localPattern, handle)
	base := "/base"
	http.Handle("PATCH "+base+"/handled", http.HandlerFunc(handle))
	conditionalPattern := "/conditional"
	if len(healthPattern) > 0 {
		conditionalPattern = "/changed"
	}
	http.HandleFunc(conditionalPattern, handle)

	var fake fakeMux
	fake.HandleFunc("/fake", handle)
	fake.Handle("/fake-handle", http.HandlerFunc(handle))
	fake.GET("/also-fake", handle)
}
`)

	parser := NewParser("example.com/routes", "routes-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	sourceFile := filepath.Join(dir, "main.go")
	sourceID := "example.com/routes/main.go"
	registerID := "example.com/routes.register"
	handlerID := "example.com/routes.handle"
	expected := map[string]string{
		sourceID + "::route:ANY:/healthz":        "ANY /healthz",
		sourceID + "::route:GET:/mux-handle":     "GET /mux-handle",
		sourceID + "::route:GET:/ready":          "GET /ready",
		sourceID + "::route:GET:/same":           "GET /same",
		sourceID + "::route:POST:/same":          "POST /same",
		sourceID + "::route:ANY:/plain-handle":   "ANY /plain-handle",
		sourceID + "::route:ANY:/local":          "ANY /local",
		sourceID + "::route:PATCH:/base/handled": "PATCH /base/handled",
	}

	nodes := nodesByID(result)
	for routeID, name := range expected {
		route := nodes[routeID]
		require.NotNil(t, route, routeID)
		assert.Equal(t, NodeTypeRoute, route.Type)
		assert.Equal(t, name, route.Name)
		assert.Equal(t, sourceFile, route.FilePath)
		assert.Equal(t, "example.com/routes", route.PkgPath)
		assert.Equal(t, "routes-repo", route.RepoID)
		assert.Positive(t, route.Lines[0])
		assert.GreaterOrEqual(t, route.Lines[1], route.Lines[0])
		assert.True(t, hasEdge(result, sourceFile, routeID, EdgeTypeContains))
		assert.True(t, hasEdge(result, registerID, routeID, EdgeTypeRegistersRoute))
		assert.True(t, hasEdge(result, routeID, handlerID, EdgeTypeReferences))

		registerEdges := edgesBy(result, registerID, routeID, EdgeTypeRegistersRoute)
		require.Len(t, registerEdges, 1)
		require.Len(t, registerEdges[0].Occurrences, 1)
		assert.Equal(t, sourceFile, registerEdges[0].Occurrences[0].FilePath)
		assert.Positive(t, registerEdges[0].Occurrences[0].Line)
		assert.Positive(t, registerEdges[0].Occurrences[0].Column)
	}

	for _, node := range result.Nodes {
		if node.Type == NodeTypeRoute {
			assert.NotContains(t, node.Name, "fake")
			assert.NotContains(t, node.Name, "conditional")
			assert.NotContains(t, node.Name, "changed")
		}
	}
	assert.Len(t, routeNodes(result), len(expected))

	incremental, err := parser.ParsePackageIncremental(context.Background(), dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, routeNodeIDs(result), routeNodeIDs(incremental))
}

func TestParseWorkspaceExtractsGinRoutesAndStaticGroupPrefixes(t *testing.T) {
	withGoBuildCache(t)

	dir := t.TempDir()
	ginDir := filepath.Join(dir, "gin")
	require.NoError(t, os.MkdirAll(ginDir, 0o755))
	writeTestFile(t, filepath.Join(dir, "go.mod"), `module example.com/ginapp

go 1.25

require github.com/gin-gonic/gin v0.0.0

replace github.com/gin-gonic/gin => ./gin
`)
	writeTestFile(t, filepath.Join(ginDir, "go.mod"), "module github.com/gin-gonic/gin\n\ngo 1.25\n")
	writeTestFile(t, filepath.Join(ginDir, "gin.go"), `package gin

type HandlerFunc func()

type RouterGroup struct{}

type Engine struct {
	*RouterGroup
}

func New() *Engine { return &Engine{RouterGroup: &RouterGroup{}} }

func (g *RouterGroup) Group(string, ...HandlerFunc) *RouterGroup { return &RouterGroup{} }
func (g *RouterGroup) GET(string, ...HandlerFunc)                {}
func (g *RouterGroup) POST(string, ...HandlerFunc)               {}
func (g *RouterGroup) PUT(string, ...HandlerFunc)                {}
func (g *RouterGroup) PATCH(string, ...HandlerFunc)              {}
func (g *RouterGroup) DELETE(string, ...HandlerFunc)             {}
func (g *RouterGroup) HEAD(string, ...HandlerFunc)               {}
func (g *RouterGroup) OPTIONS(string, ...HandlerFunc)            {}
func (g *RouterGroup) Any(string, ...HandlerFunc)                {}
func (g *RouterGroup) Handle(string, string, ...HandlerFunc)     {}
func (g *RouterGroup) Static(string, string)                     {}
func (g *RouterGroup) StaticFile(string, string)                 {}
func (g *RouterGroup) StaticFS(string, interface{})              {}
func (g *RouterGroup) StaticFileFS(string, string, interface{})  {}
	`)
	writeTestFile(t, filepath.Join(dir, "routes.go"), `package ginapp

import "github.com/gin-gonic/gin"

const adminPrefix = "/admin"

func middleware() {}
func handle()     {}

func dynamicGroup(*gin.Engine) *gin.RouterGroup { return &gin.RouterGroup{} }

func register() {
	r := gin.New()
	r.GET("/root", handle)

	api := r.Group("/api")
	api.POST("/items", middleware, handle)
	itemPath := "/from-var"
	api.GET(itemPath, handle)
	handleMethod := "PATCH"
	api.Handle(handleMethod, "/handled", handle)
	branchPath := "/branch"
	if len(adminPrefix) > 0 {
		branchPath = "/changed-branch"
	}
	api.GET(branchPath, handle)
	api.Static("/assets", "./public")
	api.StaticFile("/favicon.ico", "./favicon.ico")
	api.StaticFS("/downloads", nil)
	api.StaticFileFS("/robots.txt", "./robots.txt", nil)
	v1 := api.Group("/v1")
	v1.PUT("/items/:id", handle)
	r.Group("/inline").Group("/v2").DELETE("/items", handle)

	admin := r.Group(adminPrefix)
	admin.Any("/status", handle)

	secure := r.Group("/secure", middleware)
	secure.GET("/items", handle)
	version := "/v2"
	secure.Group(version, middleware).GET("/nested", handle)

	var options = r.Group("/opts")
	options.OPTIONS("/x", handle)

	var assigned *gin.RouterGroup
	assigned = r.Group("/assigned")
	assigned.HEAD("/x", handle)

	{
		api := r.Group("/shadow")
		api.GET("/inside", handle)
	}

	api = r.Group("/reassigned")
	api.PATCH("/ok", handle)
	api = dynamicGroup(r)
	api.PATCH("/skip", handle)

	unknown := dynamicGroup(r)
	unknown.GET("/skip", handle)

	conditional := r.Group("/conditional")
	if len(adminPrefix) > 0 {
		conditional = r.Group("/changed")
	}
	conditional.GET("/skip", handle)

	looped := r.Group("/looped")
	for i := 0; i < 1; i++ {
		looped = r.Group("/changed-in-loop")
	}
	looped.GET("/skip", handle)
}

func fromParameter(group *gin.RouterGroup) {
	group.GET("/skip", handle)
}

func closures() {
	r := gin.New()
	group := r.Group("/captured")
	func() {
		group.PATCH("/inside", handle)
	}()
	go func() {
		group.GET("/async", handle)
	}()
}
`)

	parser := NewParser("example.com/ginapp", "gin-repo")
	result, err := parser.ParseWorkspace(context.Background(), dir)
	require.NoError(t, err)

	sourceID := "example.com/ginapp/routes.go"
	registerID := "example.com/ginapp.register"
	closuresID := "example.com/ginapp.closures"
	middlewareID := "example.com/ginapp.middleware"
	handlerID := "example.com/ginapp.handle"

	type expectedRoute struct {
		ownerID string
		refs    []string
	}
	expected := map[string]expectedRoute{
		"GET:/root":                     {ownerID: registerID, refs: []string{handlerID}},
		"POST:/api/items":               {ownerID: registerID, refs: []string{middlewareID, handlerID}},
		"GET:/api/from-var":             {ownerID: registerID, refs: []string{handlerID}},
		"PATCH:/api/handled":            {ownerID: registerID, refs: []string{handlerID}},
		"GET:/api/assets/*filepath":     {ownerID: registerID},
		"HEAD:/api/assets/*filepath":    {ownerID: registerID},
		"GET:/api/favicon.ico":          {ownerID: registerID},
		"HEAD:/api/favicon.ico":         {ownerID: registerID},
		"GET:/api/downloads/*filepath":  {ownerID: registerID},
		"HEAD:/api/downloads/*filepath": {ownerID: registerID},
		"GET:/api/robots.txt":           {ownerID: registerID},
		"HEAD:/api/robots.txt":          {ownerID: registerID},
		"PUT:/api/v1/items/:id":         {ownerID: registerID, refs: []string{handlerID}},
		"DELETE:/inline/v2/items":       {ownerID: registerID, refs: []string{handlerID}},
		"ANY:/admin/status":             {ownerID: registerID, refs: []string{handlerID}},
		"GET:/secure/items":             {ownerID: registerID, refs: []string{middlewareID, handlerID}},
		"GET:/secure/v2/nested":         {ownerID: registerID, refs: []string{middlewareID, handlerID}},
		"OPTIONS:/opts/x":               {ownerID: registerID, refs: []string{handlerID}},
		"HEAD:/assigned/x":              {ownerID: registerID, refs: []string{handlerID}},
		"GET:/shadow/inside":            {ownerID: registerID, refs: []string{handlerID}},
		"PATCH:/reassigned/ok":          {ownerID: registerID, refs: []string{handlerID}},
		"PATCH:/captured/inside":        {ownerID: closuresID, refs: []string{handlerID}},
	}

	nodes := nodesByID(result)
	for routeSuffix, want := range expected {
		routeID := sourceID + "::route:" + routeSuffix
		route := nodes[routeID]
		require.NotNil(t, route, routeID)
		assert.Equal(t, NodeTypeRoute, route.Type)
		assert.True(t, hasEdge(result, want.ownerID, routeID, EdgeTypeRegistersRoute))
		for _, refID := range want.refs {
			assert.True(t, hasEdge(result, routeID, refID, EdgeTypeReferences), "%s references %s", routeID, refID)
		}
	}

	postRouteID := sourceID + "::route:POST:/api/items"
	assert.True(t, hasEdge(result, postRouteID, middlewareID, EdgeTypeReferences))
	assert.True(t, hasEdge(result, postRouteID, handlerID, EdgeTypeReferences))

	asyncRouteID := sourceID + "::route:GET:/captured/async"
	require.NotNil(t, nodes[asyncRouteID])
	var goroutineOwner string
	for _, edge := range result.Edges {
		if edge.To == asyncRouteID && edge.Type == EdgeTypeRegistersRoute {
			goroutineOwner = edge.From
			break
		}
	}
	require.NotEmpty(t, goroutineOwner)
	require.NotNil(t, nodes[goroutineOwner])
	assert.Equal(t, NodeTypeGoroutine, nodes[goroutineOwner].Type)
	assert.True(t, strings.HasPrefix(goroutineOwner, closuresID+".goroutine_L"))

	for _, forbidden := range []string{
		sourceID + "::route:PATCH:/api/skip",
		sourceID + "::route:GET:/skip",
		sourceID + "::route:GET:/changed/skip",
		sourceID + "::route:GET:/changed-in-loop/skip",
		sourceID + "::route:GET:/api/branch",
		sourceID + "::route:GET:/api/changed-branch",
	} {
		assert.Nil(t, nodes[forbidden], forbidden)
	}
	assert.Len(t, routeNodes(result), len(expected)+1)
}

func routeNodes(result *ParseResult) []*Node {
	var routes []*Node
	for _, node := range result.Nodes {
		if node.Type == NodeTypeRoute {
			routes = append(routes, node)
		}
	}
	return routes
}

func routeNodeIDs(result *ParseResult) []string {
	var ids []string
	for _, node := range routeNodes(result) {
		ids = append(ids, node.ID)
	}
	return ids
}
