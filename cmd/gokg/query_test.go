package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryWritesProgressToStderrAndJSONToStdout(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "query-db")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)

	g := graph.NewGraph(store)
	require.NoError(t, g.BuildFromParseResult(context.Background(), &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "example.com/app.Main", Type: parser.NodeTypeFunc, Name: "Main", PkgPath: "example.com/app"},
		},
	}))
	require.NoError(t, store.Close())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newQueryCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--db", dbDir, `MATCH (n:FUNC) RETURN n.Name LIMIT 1`})

	require.NoError(t, cmd.Execute())
	assert.True(t, json.Valid(stdout.Bytes()), "stdout should contain machine-readable JSON only")
	assert.Contains(t, stdout.String(), `"n.Name": "Main"`)
	assert.NotContains(t, stdout.String(), "Parsing query")
	assert.Contains(t, stderr.String(), "Parsing query")
	assert.Contains(t, stderr.String(), "Query completed")
}

func TestQueryUsesCommandContext(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "query-db")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)

	g := graph.NewGraph(store)
	require.NoError(t, g.BuildFromParseResult(context.Background(), &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "example.com/app.Main", Type: parser.NodeTypeFunc, Name: "Main", PkgPath: "example.com/app"},
		},
	}))
	require.NoError(t, store.Close())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newQueryCommand()
	cmd.SilenceUsage = true
	cmd.SetContext(ctx)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--db", dbDir, `MATCH (n:FUNC) RETURN n.Name LIMIT 1`})

	err = cmd.Execute()
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, stdout.String())
}
