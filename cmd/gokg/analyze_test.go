package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeUsesDBFlag(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(t.TempDir(), "custom-db")

	withWorkingDir(t, projectDir)

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--db", dbDir, "--gc=false"})

	require.NoError(t, cmd.Execute())
	assert.DirExists(t, dbDir)
	assert.NoDirExists(t, filepath.Join(projectDir, ".gokg"))
}

func TestAnalyzeRebuildRemovesStaleDB(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(t.TempDir(), "custom-db")
	require.NoError(t, os.MkdirAll(dbDir, 0755))
	staleFile := filepath.Join(dbDir, "STALE")
	require.NoError(t, os.WriteFile(staleFile, []byte("old data"), 0644))

	withWorkingDir(t, projectDir)

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--db", dbDir, "--rebuild", "--gc=false"})

	require.NoError(t, cmd.Execute())
	assert.NoFileExists(t, staleFile)
	assert.DirExists(t, dbDir)
}

func TestAnalyzeWorkspaceUsesPerRepoDBs(t *testing.T) {
	withGoBuildCache(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repoA := newTinyGoProjectWithModule(t, "example.com/service-a")
	repoB := newTinyGoProjectWithModule(t, "example.com/service-b")

	ws, err := workspace.Init("demo")
	require.NoError(t, err)
	require.NoError(t, ws.AddRepo("service-a", repoA))
	require.NoError(t, ws.AddRepo("service-b", repoB))

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--workspace", "demo", "--rebuild", "--gc=false"})

	require.NoError(t, cmd.Execute())
	assert.DirExists(t, ws.GetRepoDBPath("service-a"))
	assert.DirExists(t, ws.GetRepoDBPath("service-b"))

	g, err := loadWorkspaceGraph(context.Background(), "demo")
	require.NoError(t, err)

	exported, err := g.ExportJSON()
	require.NoError(t, err)
	assert.Contains(t, exported, "repo:service-a")
	assert.Contains(t, exported, "repo:service-b")

	reopenedStore, err := storage.NewBadgerStorage(ws.GetRepoDBPath("service-a"))
	require.NoError(t, err)
	require.NoError(t, reopenedStore.Close())
}

func TestAnalyzeWorkspaceRejectsModuleFlag(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--workspace", "demo", "--module", "example.com/wrong"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--module cannot be used with --workspace")
}

func TestValidateRebuildDBPath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name       string
		path       string
		explicitDB bool
		wantErr    bool
	}{
		{name: "default .gokg allowed", path: ".gokg/", wantErr: false},
		{name: "explicit custom allowed", path: "custom-db", explicitDB: true, wantErr: false},
		{name: "empty rejected", path: " ", explicitDB: true, wantErr: true},
		{name: "root rejected", path: string(filepath.Separator), explicitDB: true, wantErr: true},
		{name: "current directory rejected", path: ".", explicitDB: true, wantErr: true},
		{name: "parent basename rejected", path: "..", explicitDB: true, wantErr: true},
		{name: "home rejected", path: home, explicitDB: true, wantErr: true},
		{name: "implicit custom rejected", path: "custom-db", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRebuildDBPath(tt.path, tt.explicitDB)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRebuildBadgerDBPathRejectsFiles(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "db-file")
	require.NoError(t, os.WriteFile(filePath, []byte("not a directory"), 0644))

	err := rebuildBadgerDBPath(filePath, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-directory")
}

func newTinyGoProject(t *testing.T) string {
	t.Helper()

	return newTinyGoProjectWithModule(t, "example.com/tiny")
}

func newTinyGoProjectWithModule(t *testing.T, modulePath string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644))
	return dir
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})
}

func withGoBuildCache(t *testing.T) {
	t.Helper()

	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
}
