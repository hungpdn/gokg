package graph

import (
	"context"
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
