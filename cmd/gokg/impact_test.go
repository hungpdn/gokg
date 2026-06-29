package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImpactWritesMarkdownToStdoutAndProgressToStderr(t *testing.T) {
	repoDir, dbDir := newImpactGitRepoAndDB(t, true)
	withWorkingDir(t, repoDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newImpactCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--db", dbDir, "--depth", "2"})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "## Change Impact")
	assert.Contains(t, stdout.String(), "Changed")
	assert.Contains(t, stdout.String(), "Caller")
	assert.NotContains(t, stdout.String(), "Loading graph")
	assert.Contains(t, stderr.String(), "Loading graph")
	assert.Contains(t, stderr.String(), "Analyzing change impact")
}

func TestImpactJSONWritesMachineReadableStdout(t *testing.T) {
	repoDir, dbDir := newImpactGitRepoAndDB(t, true)
	withWorkingDir(t, repoDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newImpactCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--db", dbDir, "--json"})

	require.NoError(t, cmd.Execute())
	assert.True(t, json.Valid(stdout.Bytes()))
	assert.Contains(t, stdout.String(), `"changed_nodes"`)
	assert.NotContains(t, stdout.String(), "Loading graph")
	assert.Contains(t, stderr.String(), "Loading graph")
}

func TestImpactIncludesUntrackedByDefaultAndTrackedOnlyCanDisable(t *testing.T) {
	repoDir, dbDir := newImpactGitRepoAndDB(t, true)
	withWorkingDir(t, repoDir)
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "new.go"), []byte("package impact\n\nfunc NewFile() {}\n"), 0o644))

	var defaultOut bytes.Buffer
	defaultCmd := newImpactCommand()
	defaultCmd.SetOut(&defaultOut)
	defaultCmd.SetErr(&bytes.Buffer{})
	defaultCmd.SetArgs([]string{"--db", dbDir, "--json"})
	require.NoError(t, defaultCmd.Execute())
	assert.Contains(t, defaultOut.String(), `"path": "new.go"`)
	assert.Contains(t, defaultOut.String(), `"status": "??"`)

	var trackedOnlyOut bytes.Buffer
	trackedOnlyCmd := newImpactCommand()
	trackedOnlyCmd.SetOut(&trackedOnlyOut)
	trackedOnlyCmd.SetErr(&bytes.Buffer{})
	trackedOnlyCmd.SetArgs([]string{"--db", dbDir, "--json", "--tracked-only"})
	require.NoError(t, trackedOnlyCmd.Execute())
	assert.NotContains(t, trackedOnlyOut.String(), `"path": "new.go"`)
}

func TestImpactTrackedOnlyRejectsExplicitIncludeUntracked(t *testing.T) {
	cmd := newImpactCommand()
	cmd.SetArgs([]string{"--tracked-only", "--include-untracked"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tracked-only cannot be used with --include-untracked=true")
}

func TestImpactUsesGraphRootForCustomDB(t *testing.T) {
	_, dbDir := newImpactGitRepoAndDB(t, true)
	withWorkingDir(t, t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newImpactCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--db", dbDir, "--json"})

	require.NoError(t, cmd.Execute())
	assert.True(t, json.Valid(stdout.Bytes()))
	assert.Contains(t, stdout.String(), `"example.com/impact.Changed"`)
	assert.NotContains(t, stderr.String(), "failed")
}

func TestImpactEmptyDiffSucceeds(t *testing.T) {
	repoDir, dbDir := newImpactGitRepoAndDB(t, false)
	withWorkingDir(t, repoDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newImpactCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--db", dbDir})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "No changes detected")
}

func TestImpactWorkspaceRejectsDBFlag(t *testing.T) {
	cmd := newImpactCommand()
	cmd.SetArgs([]string{"--workspace", "demo", "--db", "custom"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--db cannot be used with --workspace")
}

func newImpactGitRepoAndDB(t *testing.T, modify bool) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for impact CLI tests")
	}

	repoDir := t.TempDir()
	appPath := filepath.Join(repoDir, "app.go")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/impact\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(appPath, []byte("package impact\n\nfunc Changed() int {\n\treturn 1\n}\n\nfunc Caller() int {\n\treturn Changed()\n}\n"), 0644))
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test User")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "initial")

	dbDir := filepath.Join(t.TempDir(), "impact-db")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)
	g := graph.NewGraph(store)
	require.NoError(t, g.BuildFromParseResult(context.Background(), &parser.ParseResult{
		Nodes: []*parser.Node{
			{ID: "folder:.", Type: parser.NodeTypeFolder, Name: filepath.Base(repoDir), FilePath: repoDir, RepoID: "example.com/impact"},
			{ID: "example.com/impact.Changed", Type: parser.NodeTypeFunc, Name: "Changed", PkgPath: "example.com/impact", FilePath: appPath, Lines: [2]int{3, 5}, RepoID: "example.com/impact"},
			{ID: "example.com/impact.Caller", Type: parser.NodeTypeFunc, Name: "Caller", PkgPath: "example.com/impact", FilePath: appPath, Lines: [2]int{7, 9}, RepoID: "example.com/impact"},
		},
		Edges: []*parser.Edge{
			{From: "example.com/impact.Caller", To: "example.com/impact.Changed", Type: parser.EdgeTypeCalls, RepoID: "example.com/impact"},
		},
	}))
	require.NoError(t, store.Close())

	if modify {
		require.NoError(t, os.WriteFile(appPath, []byte("package impact\n\nfunc Changed() int {\n\treturn 2\n}\n\nfunc Caller() int {\n\treturn Changed()\n}\n"), 0644))
	}
	return repoDir, dbDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
