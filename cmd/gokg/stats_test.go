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

func TestStatsCommandUsesDBFlag(t *testing.T) {
	dbDir := createStatsTestDB(t)

	cmd := newStatsCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", dbDir})

	require.NoError(t, cmd.Execute())
	output := out.String()
	assert.Contains(t, output, "GoKG Graph Statistics")
	assert.Contains(t, output, "DB Size:")
	assert.Contains(t, output, "Graph RAM Estimate:")
	assert.Contains(t, output, "Nodes: 3")
	assert.Contains(t, output, "Edges: 2")
	assert.Contains(t, output, "File Nodes: 1")
	assert.Contains(t, output, "Nodes by Kind:")
	assert.Contains(t, output, "FUNC")
	assert.Contains(t, output, "Edges by Kind:")
	assert.Contains(t, output, "CALLS")
}

func TestStatsCommandJSON(t *testing.T) {
	dbDir := createStatsTestDB(t)

	cmd := newStatsCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", dbDir, "--json"})

	require.NoError(t, cmd.Execute())

	var report statsReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	assert.Equal(t, `database "`+dbDir+`"`, report.Source)
	assert.Equal(t, []string{dbDir}, report.DBPaths)
	assert.Positive(t, report.DBSizeBytes)
	assert.Equal(t, 3, report.Graph.NodeCount)
	assert.Equal(t, 2, report.Graph.EdgeCount)
	assert.Equal(t, 1, report.Graph.FileNodeCount)
	assert.Equal(t, 1, report.Graph.NodesByKind["FILE"])
}

func TestStatsSourcePreservesWindowsPathBackslashes(t *testing.T) {
	dbDir := `C:\Users\RUNNER~1\AppData\Local\Temp\TestStatsCommandJSON3330498526\001\stats-db`

	assert.Equal(t, `database "`+dbDir+`"`, statsSource("database", dbDir))
}

func createStatsTestDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dbDir := filepath.Join(t.TempDir(), "stats-db")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)

	g := graph.NewGraph(store)
	nodes := []*parser.Node{
		{ID: "file", Type: parser.NodeTypeFile, Name: "main.go", FilePath: "/tmp/main.go", PkgPath: "example.com/app"},
		{ID: "fn", Type: parser.NodeTypeFunc, Name: "main", FilePath: "/tmp/main.go", PkgPath: "example.com/app"},
		{ID: "dep", Type: parser.NodeTypeBoundary, Name: "fmt.Println"},
	}
	for _, node := range nodes {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "file", To: "fn", Type: parser.EdgeTypeContains}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "fn", To: "dep", Type: parser.EdgeTypeCalls}))
	require.NoError(t, store.Close())

	return dbDir
}
