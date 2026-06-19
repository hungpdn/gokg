package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportMermaid(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	n1 := parser.NewNode()
	n1.ID = "pkg.A"
	n1.Name = "A"

	n2 := parser.NewNode()
	n2.ID = "pkg.B"
	n2.Name = "B"

	_, err := g.AddNode(ctx, n1)
	require.NoError(t, err)
	_, err = g.AddNode(ctx, n2)
	require.NoError(t, err)

	e := parser.NewEdge()
	e.From = "pkg.A"
	e.To = "pkg.B"
	e.Type = parser.EdgeTypeCalls
	err = g.AddEdge(ctx, e)
	require.NoError(t, err)

	mermaid := g.ExportMermaid()
	assert.Contains(t, mermaid, "flowchart TD")
	assert.Contains(t, mermaid, "pkg_A[\"A\"]")
	assert.Contains(t, mermaid, "pkg_B[\"B\"]")
	assert.Contains(t, mermaid, "pkg_A -->|CALLS| pkg_B")
}

func TestExportMermaidSanitizesWorkspaceIDsAndLabels(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	n1 := parser.NewNode()
	n1.ID = "workspace:demo"
	n1.Name = "demo"
	n1.Type = parser.NodeTypeWorkspace

	n2 := parser.NewNode()
	n2.ID = "repo:lvl2:folder:."
	n2.Name = "repo \"lvl2\"\nroot"
	n2.Type = parser.NodeTypeFolder

	_, err := g.AddNode(ctx, n1)
	require.NoError(t, err)
	_, err = g.AddNode(ctx, n2)
	require.NoError(t, err)

	e := parser.NewEdge()
	e.From = n1.ID
	e.To = n2.ID
	e.Type = parser.EdgeTypeContains
	require.NoError(t, g.AddEdge(ctx, e))

	mermaid := g.ExportMermaid()
	assert.Contains(t, mermaid, "workspace_demo[\"demo:WORKSPACE\"]")
	assert.Contains(t, mermaid, `repo_lvl2_folder__["repo \"lvl2\"\nroot:FOLDER"]`)
	assert.Contains(t, mermaid, "workspace_demo -->|CONTAINS| repo_lvl2_folder__")
	assert.NotContains(t, mermaid, "repo:lvl2:folder:.")
}

func TestExportDot(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	n1 := parser.NewNode()
	n1.ID = "A"
	n1.Name = "A"

	n2 := parser.NewNode()
	n2.ID = "B"
	n2.Name = "B"

	_, _ = g.AddNode(ctx, n1)
	_, _ = g.AddNode(ctx, n2)

	e := parser.NewEdge()
	e.From = "A"
	e.To = "B"
	e.Type = parser.EdgeTypeCalls
	_ = g.AddEdge(ctx, e)

	dot := g.ExportDot()
	assert.Contains(t, dot, "digraph G {")
	assert.Contains(t, dot, "\"A\" [label=\"A\"];")
	assert.Contains(t, dot, "\"B\" [label=\"B\"];")
	assert.Contains(t, dot, "\"A\" -> \"B\" [label=\"CALLS\"];")
}

func TestExportDotIncludesCallOccurrences(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	_, err := g.AddNode(ctx, &parser.Node{ID: "A", Name: "A"})
	require.NoError(t, err)
	_, err = g.AddNode(ctx, &parser.Node{ID: "B", Name: "B"})
	require.NoError(t, err)

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{
		From: "A",
		To:   "B",
		Type: parser.EdgeTypeCalls,
		Occurrences: []parser.EdgeOccurrence{
			{FilePath: "main.go", Line: 10, Column: 2},
			{FilePath: "main.go", Line: 12, Column: 4},
		},
	}))

	dot := g.ExportDot()
	assert.Contains(t, dot, "\"A\" -> \"B\" [label=\"CALLS\", occurrences=\"2\", lines=\"main.go:10:2,main.go:12:4\"];")
}

func TestExportJSON(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	n1 := parser.NewNode()
	n1.ID = "A"
	n1.Name = "A"

	_, _ = g.AddNode(ctx, n1)

	js, err := g.ExportJSON()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(js, "{"))
	assert.Contains(t, js, "\"ID\": \"A\"")
}

func TestExportJSONTo(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	_, err := g.AddNode(ctx, &parser.Node{ID: "A", Name: "A"})
	require.NoError(t, err)
	_, err = g.AddNode(ctx, &parser.Node{ID: "B", Name: "B"})
	require.NoError(t, err)
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "A", To: "B", Type: parser.EdgeTypeCalls}))

	var b strings.Builder
	require.NoError(t, g.ExportJSONTo(&b))

	var decoded struct {
		Nodes []parser.Node `json:"nodes"`
		Edges []parser.Edge `json:"edges"`
	}
	require.NoError(t, json.Unmarshal([]byte(b.String()), &decoded))
	require.Len(t, decoded.Nodes, 2)
	require.Len(t, decoded.Edges, 1)
	assert.Equal(t, parser.EdgeTypeCalls, decoded.Edges[0].Type)
}
