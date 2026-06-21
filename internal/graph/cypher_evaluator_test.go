package graph

import (
	"context"
	"testing"

	"github.com/hungpdn/gokg/internal/cypher"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteCypher(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	n1 := &parser.Node{ID: "funcA", Type: parser.NodeTypeFunc, Name: "A", PkgPath: "example/parser", RepoID: "repo-a"}
	n2 := &parser.Node{ID: "funcB", Type: parser.NodeTypeFunc, Name: "B", PkgPath: "example/graph", RepoID: "repo-a"}
	n3 := &parser.Node{ID: "structX", Type: parser.NodeTypeStruct, Name: "X", RepoID: "repo-a"}

	_, err := g.AddNode(ctx, n1)
	require.NoError(t, err)
	_, err = g.AddNode(ctx, n2)
	require.NoError(t, err)
	_, err = g.AddNode(ctx, n3)
	require.NoError(t, err)

	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA", To: "funcB", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcB", To: "structX", Type: parser.EdgeTypeContains}))

	qb := g.Query()

	t.Run("Match All Funcs", func(t *testing.T) {
		input := `MATCH (n:FUNC) RETURN n.Name`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		assert.Len(t, res, 2)
	})

	t.Run("Match Node Where", func(t *testing.T) {
		input := `MATCH (n:FUNC) WHERE n.Name = "A" RETURN n`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		assert.Len(t, res, 1)

		node, ok := res[0]["n"].(*parser.Node)
		require.True(t, ok)
		assert.Equal(t, "A", node.Name)
	})

	t.Run("Match Edges", func(t *testing.T) {
		input := `MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) RETURN a.Name, b.Name`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		assert.Len(t, res, 1)

		assert.Equal(t, "A", res[0]["a.Name"])
		assert.Equal(t, "B", res[0]["b.Name"])
	})

	t.Run("Match Any Direction", func(t *testing.T) {
		input := `MATCH (a)-[r]-(b) WHERE a.Name = "B" RETURN b.Name`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		// B has incoming from A, outbound to X
		assert.Len(t, res, 2)
	})

	t.Run("Lowercase Contains And Node Type", func(t *testing.T) {
		input := `match (n:func) where n.PkgPath contains "parser" return n.Name`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "A", res[0]["n.Name"])
	})

	t.Run("Edge Where And Return Properties", func(t *testing.T) {
		input := `MATCH (a:FUNC)-[r]->(b:FUNC) WHERE r.Type = "CALLS" RETURN r.Type, r.From, r.To`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "CALLS", res[0]["r.Type"])
		assert.Equal(t, "funcA", res[0]["r.From"])
		assert.Equal(t, "funcB", res[0]["r.To"])
	})

	t.Run("Match Self Edge", func(t *testing.T) {
		require.NoError(t, g.AddEdge(context.Background(), &parser.Edge{From: "funcA", To: "funcA", Type: parser.EdgeTypeCalls}))

		input := `MATCH (a:FUNC)-[r:CALLS]->(b:FUNC) WHERE a.Name = "A" RETURN b.Name`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		res, err := qb.ExecuteCypher(q)
		require.NoError(t, err)
		var names []string
		for _, row := range res {
			names = append(names, row["b.Name"].(string))
		}
		assert.Contains(t, names, "A")
	})

	t.Run("Reject Unknown Where Alias", func(t *testing.T) {
		input := `MATCH (n:FUNC) WHERE x.Name = "A" RETURN n`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		_, err = qb.ExecuteCypher(q)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown alias "x" in WHERE`)
	})

	t.Run("Reject Unknown Return Property", func(t *testing.T) {
		input := `MATCH (n:FUNC) RETURN n.Package`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		_, err = qb.ExecuteCypher(q)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown property "Package"`)
	})

	t.Run("Reject Unknown Node Type", func(t *testing.T) {
		input := `MATCH (n:CLASS) RETURN n`
		q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
		require.NoError(t, err)

		_, err = qb.ExecuteCypher(q)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown node type "CLASS"`)
	})
}

func TestExecuteCypher_EdgeWhereChecksAllEdgesBetweenPair(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()
	n1 := &parser.Node{ID: "funcA", Type: parser.NodeTypeFunc, Name: "A"}
	n2 := &parser.Node{ID: "funcB", Type: parser.NodeTypeFunc, Name: "B"}

	_, err := g.AddNode(ctx, n1)
	require.NoError(t, err)
	_, err = g.AddNode(ctx, n2)
	require.NoError(t, err)
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA", To: "funcB", Type: parser.EdgeTypeCalls}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "funcA", To: "funcB", Type: parser.EdgeTypeImports}))

	input := `MATCH (a:FUNC)-[r]->(b:FUNC) WHERE r.Type = "IMPORTS" RETURN r.Type`
	q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
	require.NoError(t, err)

	res, err := g.Query().ExecuteCypher(q)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "IMPORTS", res[0]["r.Type"])
}

func TestExecuteCypherLimitReturnsProjectedRows(t *testing.T) {
	g := NewGraph(nil)
	ctx := context.Background()

	for _, node := range []*parser.Node{
		{ID: "funcA", Type: parser.NodeTypeFunc, Name: "A"},
		{ID: "funcB", Type: parser.NodeTypeFunc, Name: "B"},
		{ID: "funcC", Type: parser.NodeTypeFunc, Name: "C"},
	} {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}

	input := `MATCH (n:FUNC) RETURN n.Name LIMIT 2`
	q, err := cypher.NewParser(cypher.NewLexer(input)).ParseQuery()
	require.NoError(t, err)

	res, err := g.Query().ExecuteCypher(q)
	require.NoError(t, err)
	require.Len(t, res, 2)
	for _, row := range res {
		assert.Contains(t, row, "n.Name")
	}
}
