package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	telemetrypkg "github.com/hungpdn/gokg/internal/telemetry"
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

func TestAnalyzePrintsGraphSummary(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(t.TempDir(), "custom-db")

	withWorkingDir(t, projectDir)

	var out bytes.Buffer
	cmd := newAnalyzeCommand()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--db", dbDir, "--gc=false"})

	require.NoError(t, cmd.Execute())

	output := out.String()
	assert.Contains(t, output, "Graph Summary:")
	assert.Contains(t, output, "Nodes")
	assert.Contains(t, output, "Edges")
	assert.Contains(t, output, "Analysis time")
	assert.Contains(t, output, "Nodes by Kind:")
	assert.Contains(t, output, "Edges by Kind:")
	assert.Contains(t, output, dbDir)
	assert.NotContains(t, output, "Parsed ")
}

func TestAnalyzeTestsFlagDefaultsToFalse(t *testing.T) {
	cmd := newAnalyzeCommand()
	flag := cmd.Flags().Lookup("tests")

	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestAnalyzeWritesAnalysisMetadata(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(t.TempDir(), "custom-db")

	withWorkingDir(t, projectDir)

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--db", dbDir, "--gc=false"})
	require.NoError(t, cmd.Execute())

	store, err := storage.NewBadgerStorageReadOnly(dbDir)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, store.Close())
	}()
	meta, ok, err := graph.LoadAnalysisMetadata(context.Background(), store)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "example.com/tiny", meta.RepoID)
	assert.Equal(t, "example.com/tiny", meta.ModulePrefix)
	wantRoot := projectDir
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		wantRoot = resolved
	}
	assert.Equal(t, filepath.Clean(wantRoot), meta.RepoRoot)
	assert.False(t, meta.IncludeTests)
	assert.False(t, meta.AnalyzedAt.IsZero())
}

func TestAnalyzeRebuildRemovesStaleDB(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(t.TempDir(), "custom-db")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	staleFile := filepath.Join(dbDir, "STALE")
	require.NoError(t, os.WriteFile(staleFile, []byte("old data"), 0644))

	withWorkingDir(t, projectDir)

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--db", dbDir, "--rebuild", "--gc=false"})

	require.NoError(t, cmd.Execute())
	assert.NoFileExists(t, staleFile)
	assert.DirExists(t, dbDir)
}

func TestAnalyzeRebuildPreservesTelemetryArtifacts(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(projectDir, ".gokg")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	base := filepath.Base(telemetrypkg.DefaultFile)
	wantFiles := map[string][]byte{
		base:        []byte("{\"tool_name\":\"current\"}\n"),
		base + ".1": []byte("{\"tool_name\":\"rotated-1\"}\n"),
		base + ".2": []byte("{\"tool_name\":\"rotated-2\"}\n"),
	}
	for name, content := range wantFiles {
		require.NoError(t, os.WriteFile(filepath.Join(dbDir, name), content, 0o600))
	}
	staleFile := filepath.Join(dbDir, "STALE")
	require.NoError(t, os.WriteFile(staleFile, []byte("old data"), 0o644))

	withWorkingDir(t, projectDir)
	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--rebuild", "--gc=false"})

	require.NoError(t, cmd.Execute())
	assert.NoFileExists(t, staleFile)
	for name, want := range wantFiles {
		got, err := os.ReadFile(filepath.Join(dbDir, name))
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	reopened, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	stagingDirs, err := filepath.Glob(filepath.Join(projectDir, ".gokg-telemetry-rebuild-*"))
	require.NoError(t, err)
	assert.Empty(t, stagingDirs)
}

func TestAnalyzeRebuildAllowsTelemetryOnlyDefaultDirectory(t *testing.T) {
	withGoBuildCache(t)
	projectDir := newTinyGoProject(t)
	dbDir := filepath.Join(projectDir, ".gokg")
	require.NoError(t, os.MkdirAll(dbDir, 0o700))
	telemetryPath := filepath.Join(dbDir, filepath.Base(telemetrypkg.DefaultFile))
	want := []byte("{\"tool_name\":\"before-analysis\"}\n")
	require.NoError(t, os.WriteFile(telemetryPath, want, 0o600))

	withWorkingDir(t, projectDir)
	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--rebuild", "--gc=false"})

	require.NoError(t, cmd.Execute())
	got, err := os.ReadFile(telemetryPath)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.FileExists(t, filepath.Join(dbDir, "MANIFEST"))
}

func TestRebuildBadgerDBPathPreservesTelemetryArtifactsForCustomDB(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "custom-db")
	store, err := storage.NewBadgerStorage(dbDir)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	base := filepath.Base(telemetrypkg.DefaultFile)
	wantFiles := map[string][]byte{
		base:        []byte("active\n"),
		base + ".3": []byte("oldest\n"),
	}
	for name, content := range wantFiles {
		require.NoError(t, os.WriteFile(filepath.Join(dbDir, name), content, 0o600))
	}

	require.NoError(t, rebuildBadgerDBPath(dbDir, true))
	for name, want := range wantFiles {
		got, err := os.ReadFile(filepath.Join(dbDir, name))
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	assert.NoFileExists(t, filepath.Join(dbDir, "MANIFEST"))
	stagingDirs, err := filepath.Glob(filepath.Join(filepath.Dir(dbDir), ".custom-db-telemetry-rebuild-*"))
	require.NoError(t, err)
	assert.Empty(t, stagingDirs)
}

func TestRebuildBadgerDBPathRejectsUnsafeTelemetryArtifacts(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dbDir string) string
	}{
		{
			name: "directory",
			setup: func(t *testing.T, dbDir string) string {
				t.Helper()
				path := filepath.Join(dbDir, filepath.Base(telemetrypkg.DefaultFile))
				require.NoError(t, os.Mkdir(path, 0o700))
				return path
			},
		},
		{
			name: "symlinked rotation segment",
			setup: func(t *testing.T, dbDir string) string {
				t.Helper()
				target := filepath.Join(t.TempDir(), "outside.jsonl")
				require.NoError(t, os.WriteFile(target, []byte("outside"), 0o600))
				path := filepath.Join(dbDir, filepath.Base(telemetrypkg.DefaultFile)+".1")
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
				return path
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbDir := filepath.Join(t.TempDir(), "db")
			store, err := storage.NewBadgerStorage(dbDir)
			require.NoError(t, err)
			require.NoError(t, store.Close())
			artifactPath := tt.setup(t, dbDir)

			err = rebuildBadgerDBPath(dbDir, true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not a regular file")
			assert.DirExists(t, dbDir)
			_, statErr := os.Lstat(artifactPath)
			assert.NoError(t, statErr)
			assert.FileExists(t, filepath.Join(dbDir, "MANIFEST"))
		})
	}
}

func TestRestoreRebuildTelemetryArtifactsDoesNotOverwriteConcurrentFile(t *testing.T) {
	root := t.TempDir()
	rebuildPath := filepath.Join(root, "db")
	stagingDir := filepath.Join(root, "staging")
	require.NoError(t, os.MkdirAll(rebuildPath, 0o700))
	require.NoError(t, os.MkdirAll(stagingDir, 0o700))
	name := filepath.Base(telemetrypkg.DefaultFile)
	stagedPath := filepath.Join(stagingDir, name)
	destinationPath := filepath.Join(rebuildPath, name)
	require.NoError(t, os.WriteFile(stagedPath, []byte("preserved"), 0o600))
	require.NoError(t, os.WriteFile(destinationPath, []byte("concurrent"), 0o600))

	err := restoreRebuildTelemetryArtifacts(rebuildPath, stagingDir, []string{name})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to overwrite")
	staged, readErr := os.ReadFile(stagedPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("preserved"), staged)
	destination, readErr := os.ReadFile(destinationPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("concurrent"), destination)
}

func TestAnalyzeResolvesSingleNestedModule(t *testing.T) {
	withGoBuildCache(t)
	parentDir := t.TempDir()
	moduleDir := filepath.Join(parentDir, "services", "api")
	writeTinyGoModule(t, moduleDir, "example.com/api")
	dbDir := filepath.Join(t.TempDir(), "custom-db")

	withWorkingDir(t, parentDir)

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--db", dbDir, "--gc=false"})

	require.NoError(t, cmd.Execute())
	assert.DirExists(t, dbDir)
}

func TestResolveGoAnalysisRootRejectsMultipleNestedModules(t *testing.T) {
	parentDir := t.TempDir()
	writeTinyGoModule(t, filepath.Join(parentDir, "service-a"), "example.com/service-a")
	writeTinyGoModule(t, filepath.Join(parentDir, "service-b"), "example.com/service-b")

	_, err := resolveGoAnalysisRoot(parentDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple Go modules found")
	assert.Contains(t, err.Error(), "service-a")
	assert.Contains(t, err.Error(), "service-b")
}

func TestAnalyzeWorkspaceUsesPerRepoDBs(t *testing.T) {
	withGoBuildCache(t)
	withTestHome(t)

	repoA := newTinyGoProjectWithModule(t, "example.com/service-a")
	repoB := newTinyGoProjectWithModule(t, "example.com/service-b")

	ws, err := workspace.Init("demo")
	require.NoError(t, err)
	require.NoError(t, ws.AddRepo("service-a", repoA))
	require.NoError(t, ws.AddRepo("service-b", repoB))

	var out bytes.Buffer
	cmd := newAnalyzeCommand()
	cmd.SetOut(&out)
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

	output := out.String()
	assert.Contains(t, output, "Workspace Graph Summary: demo")
	assert.Contains(t, output, "Repo")
	assert.Contains(t, output, "Nodes")
	assert.Contains(t, output, "Edges")
	assert.Contains(t, output, "Time")
	assert.Contains(t, output, "TOTAL")
	assert.NotContains(t, output, "parsed")
}

func TestAnalyzeWorkspaceRejectsModuleFlag(t *testing.T) {
	withTestHome(t)

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

func TestRebuildBadgerDBPathRejectsSymlinkedDBRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real-db")
	require.NoError(t, os.Mkdir(target, 0o700))
	marker := filepath.Join(target, "important")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
	link := filepath.Join(root, "linked-db")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := rebuildBadgerDBPath(link, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlinked db path")
	assert.FileExists(t, marker)
	info, statErr := os.Lstat(link)
	require.NoError(t, statErr)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestRebuildBadgerDBPathRejectsNonBadgerDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-db")
	require.NoError(t, os.MkdirAll(dir, 0755))
	keepFile := filepath.Join(dir, "keep.txt")
	require.NoError(t, os.WriteFile(keepFile, []byte("important"), 0644))

	err := rebuildBadgerDBPath(dir, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not look like a complete GoKG BadgerDB database")
	assert.FileExists(t, keepFile)
}

func TestRebuildBadgerDBPathRejectsPartialBadgerMarkers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-quite-a-db")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MANIFEST"), []byte("not badger"), 0644))
	keepFile := filepath.Join(dir, "keep.txt")
	require.NoError(t, os.WriteFile(keepFile, []byte("important"), 0644))

	err := rebuildBadgerDBPath(dir, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not look like a complete GoKG BadgerDB database")
	assert.FileExists(t, keepFile)
}

func TestRebuildBadgerDBPathAllowsEmptyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty-db")
	require.NoError(t, os.MkdirAll(dir, 0755))

	require.NoError(t, rebuildBadgerDBPath(dir, true))
	assert.NoDirExists(t, dir)
}

func TestRebuildBadgerDBPathRejectsActiveTelemetryWriter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	telemetryPath := filepath.Join(dir, filepath.Base(telemetrypkg.DefaultFile))
	recorder, err := telemetrypkg.NewJSONLRecorder(telemetryPath)
	require.NoError(t, err)
	defer func() {
		if recorder != nil {
			_ = recorder.Close()
		}
	}()
	require.NoError(t, recorder.Record(context.Background(), telemetrypkg.Event{
		SessionID: "session", Transport: "stdio", ToolName: "search_nodes", Success: true,
	}))

	err = rebuildBadgerDBPath(dir, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "telemetry file may be active")
	assert.DirExists(t, dir)
	report, reportErr := telemetrypkg.BuildReportFromJSONL(telemetryPath)
	require.NoError(t, reportErr)
	assert.Equal(t, uint64(1), report.TotalCalls)

	require.NoError(t, recorder.Close())
	recorder = nil
	require.NoError(t, rebuildBadgerDBPath(dir, true))
	report, reportErr = telemetrypkg.BuildReportFromJSONL(telemetryPath)
	require.NoError(t, reportErr)
	assert.Equal(t, uint64(1), report.TotalCalls)
}

func TestAnalyzeWorkspaceRebuildRejectsNonBadgerDirectory(t *testing.T) {
	withGoBuildCache(t)
	withTestHome(t)

	repoDir := newTinyGoProjectWithModule(t, "example.com/service-a")
	ws, err := workspace.Init("demo")
	require.NoError(t, err)
	require.NoError(t, ws.AddRepo("service-a", repoDir))

	dbDir := ws.GetRepoDBPath("service-a")
	require.NoError(t, os.MkdirAll(dbDir, 0755))
	keepFile := filepath.Join(dbDir, "keep.txt")
	require.NoError(t, os.WriteFile(keepFile, []byte("important"), 0644))

	cmd := newAnalyzeCommand()
	cmd.SetArgs([]string{"--workspace", "demo", "--rebuild", "--gc=false"})

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not look like a complete GoKG BadgerDB database")
	assert.FileExists(t, keepFile)
}

func newTinyGoProject(t *testing.T) string {
	t.Helper()

	return newTinyGoProjectWithModule(t, "example.com/tiny")
}

func newTinyGoProjectWithModule(t *testing.T, modulePath string) string {
	t.Helper()

	dir := t.TempDir()
	writeTinyGoModule(t, dir, modulePath)
	return dir
}

func writeTinyGoModule(t *testing.T, dir string, modulePath string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644))
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

func withTestHome(t *testing.T) string {
	t.Helper()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	volume := filepath.VolumeName(tmpHome)
	homePath := tmpHome
	if volume != "" {
		homePath = tmpHome[len(volume):]
	}
	t.Setenv("HOMEDRIVE", volume)
	t.Setenv("HOMEPATH", homePath)

	return tmpHome
}
