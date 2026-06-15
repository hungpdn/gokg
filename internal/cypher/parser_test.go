package cypher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQuery(t *testing.T) {
	input := `MATCH (n:FUNC)-[r:CALLS]->(m:FUNC) WHERE n.Name = "main" RETURN n, m.PkgPath LIMIT 10`
	l := NewLexer(input)
	p := NewParser(l)

	q, err := p.ParseQuery()
	require.NoError(t, err)

	// Check Match
	require.NotNil(t, q.Match)
	require.NotNil(t, q.Match.Pattern)
	assert.Equal(t, "n", q.Match.Pattern.Node1.Alias)
	assert.Equal(t, "FUNC", q.Match.Pattern.Node1.Type)

	require.NotNil(t, q.Match.Pattern.Edge)
	assert.Equal(t, "r", q.Match.Pattern.Edge.Alias)
	assert.Equal(t, "CALLS", q.Match.Pattern.Edge.Type)
	assert.Equal(t, DirOutbound, q.Match.Pattern.Edge.Direction)

	require.NotNil(t, q.Match.Pattern.Node2)
	assert.Equal(t, "m", q.Match.Pattern.Node2.Alias)
	assert.Equal(t, "FUNC", q.Match.Pattern.Node2.Type)

	// Check Where
	require.NotNil(t, q.Where)
	require.Len(t, q.Where.Conditions, 1)
	assert.Equal(t, "n", q.Where.Conditions[0].Alias)
	assert.Equal(t, "Name", q.Where.Conditions[0].Property)
	assert.Equal(t, "=", q.Where.Conditions[0].Operator)
	assert.Equal(t, "main", q.Where.Conditions[0].Value)

	// Check Return
	require.NotNil(t, q.Return)
	require.Len(t, q.Return.Items, 2)
	assert.Equal(t, "n", q.Return.Items[0].Alias)
	assert.Equal(t, "", q.Return.Items[0].Property)
	assert.Equal(t, "m", q.Return.Items[1].Alias)
	assert.Equal(t, "PkgPath", q.Return.Items[1].Property)

	// Check Limit
	assert.Equal(t, 10, q.Limit)
}

func TestParseQuery_Simple(t *testing.T) {
	input := `MATCH (pkg:PACKAGE) RETURN pkg`
	l := NewLexer(input)
	p := NewParser(l)

	q, err := p.ParseQuery()
	require.NoError(t, err)

	assert.Equal(t, "pkg", q.Match.Pattern.Node1.Alias)
	assert.Equal(t, "PACKAGE", q.Match.Pattern.Node1.Type)
	assert.Nil(t, q.Match.Pattern.Edge)
	assert.Nil(t, q.Match.Pattern.Node2)

	assert.Nil(t, q.Where)

	require.Len(t, q.Return.Items, 1)
	assert.Equal(t, "pkg", q.Return.Items[0].Alias)
}

func TestParseQuery_ExplicitAndAndCaseNormalization(t *testing.T) {
	input := `match (n:func) where n.Name contains "main" AND n.Type != "BOUNDARY" return n.Name limit 5`
	l := NewLexer(input)
	p := NewParser(l)

	q, err := p.ParseQuery()
	require.NoError(t, err)

	assert.Equal(t, "FUNC", q.Match.Pattern.Node1.Type)
	require.NotNil(t, q.Where)
	require.Len(t, q.Where.Conditions, 2)
	assert.Equal(t, "CONTAINS", q.Where.Conditions[0].Operator)
	assert.Equal(t, "!=", q.Where.Conditions[1].Operator)
	assert.Equal(t, 5, q.Limit)
}

func TestParseQuery_RejectsTrailingTokens(t *testing.T) {
	input := `MATCH (n:FUNC) RETURN n WHERE n.Name = "main"`
	l := NewLexer(input)
	p := NewParser(l)

	_, err := p.ParseQuery()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected token after query")
}

func TestParseQuery_AllowsAnonymousAnyDirectionEdge(t *testing.T) {
	input := `MATCH (a)--(b) RETURN b`
	l := NewLexer(input)
	p := NewParser(l)

	q, err := p.ParseQuery()
	require.NoError(t, err)
	require.NotNil(t, q.Match.Pattern.Edge)
	assert.Equal(t, DirAny, q.Match.Pattern.Edge.Direction)
	assert.Equal(t, "b", q.Match.Pattern.Node2.Alias)
}
