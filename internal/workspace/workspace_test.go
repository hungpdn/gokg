package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitAndLoad(t *testing.T) {
	setTestHome(t)

	ws, err := Init("test-ws")
	require.NoError(t, err)
	assert.Equal(t, "test-ws", ws.Name)
	assert.NotEmpty(t, ws.Dir)
	assert.Empty(t, ws.Config.Repos)

	// Verify config file exists
	configPath := filepath.Join(ws.Dir, configFileName)
	_, err = os.Stat(configPath)
	assert.NoError(t, err)

	// Load the workspace
	loaded, err := Load("test-ws")
	require.NoError(t, err)
	assert.Equal(t, "test-ws", loaded.Name)
	assert.Equal(t, ws.Dir, loaded.Dir)

	// Init again should fail
	_, err = Init("test-ws")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestAddRepo(t *testing.T) {
	setTestHome(t)

	ws, err := Init("multi-repo-ws")
	require.NoError(t, err)

	err = ws.AddRepo("github.com/org/service-a", "/path/to/service-a")
	require.NoError(t, err)

	err = ws.AddRepo("github.com/org/service-b", "/path/to/service-b")
	require.NoError(t, err)

	assert.Len(t, ws.Config.Repos, 2)
	assert.Equal(t, "/path/to/service-a", ws.Config.Repos["github.com/org/service-a"])
	assert.Equal(t, "/path/to/service-b", ws.Config.Repos["github.com/org/service-b"])

	// Reload and verify persistence
	loaded, err := Load("multi-repo-ws")
	require.NoError(t, err)
	assert.Len(t, loaded.Config.Repos, 2)
}

func TestAddRepoRejectsDuplicateID(t *testing.T) {
	setTestHome(t)

	ws, err := Init("duplicate-repo-ws")
	require.NoError(t, err)

	require.NoError(t, ws.AddRepo("github.com/org/service-a", "/path/to/service-a"))
	err = ws.AddRepo("github.com/org/service-a", "/another/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Equal(t, "/path/to/service-a", ws.Config.Repos["github.com/org/service-a"])
}

func TestAddRepoRejectsDBPathCollision(t *testing.T) {
	setTestHome(t)

	ws, err := Init("collision-repo-ws")
	require.NoError(t, err)

	require.NoError(t, ws.AddRepo("github.com/org/service-a", "/path/to/service-a"))
	err = ws.AddRepo("github.com_org_service-a", "/path/to/service-a-copy")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database path collides")
	assert.NotContains(t, ws.Config.Repos, "github.com_org_service-a")
}

func TestGetRepoDBPath(t *testing.T) {
	setTestHome(t)

	ws, err := Init("db-path-ws")
	require.NoError(t, err)

	dbPath := ws.GetRepoDBPath("github.com/org/service-a")
	assert.Contains(t, dbPath, "github.com_org_service-a.db")
	assert.True(t, filepath.IsAbs(dbPath))
}

func TestLoadNonExistent(t *testing.T) {
	setTestHome(t)

	_, err := Load("does-not-exist")
	assert.Error(t, err)
}

func TestLoadRejectsUnsafeConfig(t *testing.T) {
	tmpHome := setTestHome(t)

	wsDir := filepath.Join(tmpHome, workspaceDirName, "unsafe-ws")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	config := `{
  "name": "unsafe-ws",
  "repos": {
    "github.com/org/service-a": "/path/to/service-a",
    "github.com_org_service-a": "/path/to/service-a-copy"
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, configFileName), []byte(config), 0o644))

	_, err := Load("unsafe-ws")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database path collides")
}

func TestLoadRejectsWorkspaceNameMismatch(t *testing.T) {
	tmpHome := setTestHome(t)

	wsDir := filepath.Join(tmpHome, workspaceDirName, "actual-ws")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	config := `{"name":"other-ws","repos":{}}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, configFileName), []byte(config), 0o644))

	_, err := Load("actual-ws")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestRemoveRepo(t *testing.T) {
	setTestHome(t)

	ws, err := Init("remove-test")
	require.NoError(t, err)

	require.NoError(t, ws.AddRepo("repo-a", "/a"))
	require.NoError(t, ws.AddRepo("repo-b", "/b"))
	assert.Len(t, ws.Config.Repos, 2)

	repoADBPath := ws.GetRepoDBPath("repo-a")
	require.NoError(t, os.MkdirAll(repoADBPath, 0755))

	err = ws.RemoveRepo("repo-a")
	require.NoError(t, err)
	assert.Len(t, ws.Config.Repos, 1)
	assert.Empty(t, ws.Config.Repos["repo-a"])
	assert.Equal(t, "/b", ws.Config.Repos["repo-b"])
	assert.NoDirExists(t, repoADBPath)
}

func TestListWorkspaces(t *testing.T) {
	setTestHome(t)

	_, err := Init("ws-alpha")
	require.NoError(t, err)
	_, err = Init("ws-beta")
	require.NoError(t, err)

	names, err := List()
	require.NoError(t, err)
	assert.Equal(t, []string{"ws-alpha", "ws-beta"}, names)
}

func TestRejectsInvalidWorkspaceNames(t *testing.T) {
	setTestHome(t)

	_, err := Init("../escape")
	assert.Error(t, err)

	_, err = Load("../escape")
	assert.Error(t, err)
}

func setTestHome(t *testing.T) string {
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
