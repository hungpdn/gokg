package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hungpdn/gokg/internal/storage"
	telemetrypkg "github.com/hungpdn/gokg/internal/telemetry"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPHTTPURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8080/mcp", mcpHTTPURL("", ""))
	assert.Equal(t, "http://127.0.0.1:9090/mcp", mcpHTTPURL(":9090", "mcp"))
	assert.Equal(t, "http://0.0.0.0:8080/api/mcp", mcpHTTPURL("0.0.0.0:8080", "/api/mcp"))
}

func TestMCPTelemetryFlagValidation(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErrText string
	}{
		{name: "disabled defaults"},
		{name: "enabled defaults", args: []string{"--telemetry"}},
		{name: "file requires telemetry", args: []string{"--telemetry-file", "events.jsonl"}, wantErrText: "requires --telemetry"},
		{name: "explicit false with file", args: []string{"--telemetry=false", "--telemetry-file", "events.jsonl"}, wantErrText: "requires --telemetry"},
		{name: "rotation bytes require telemetry", args: []string{"--telemetry-max-bytes", "1024"}, wantErrText: "requires --telemetry"},
		{name: "rotation backups require telemetry", args: []string{"--telemetry-max-backups", "2"}, wantErrText: "requires --telemetry"},
		{name: "blank file", args: []string{"--telemetry", "--telemetry-file", "  "}, wantErrText: "must not be empty"},
		{name: "negative max bytes", args: []string{"--telemetry", "--telemetry-max-bytes", "-1"}, wantErrText: "greater than or equal to 0"},
		{name: "negative max backups", args: []string{"--telemetry", "--telemetry-max-backups", "-1"}, wantErrText: "greater than or equal to 0"},
		{name: "unexpected positional argument", args: []string{"unexpected"}, wantErrText: "unknown command"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newMCPTelemetryValidationTestCommand()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if tt.wantErrText == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrText)
		})
	}
}

func TestValidateMCPTelemetryFilePath(t *testing.T) {
	repoDir := t.TempDir()
	dbDir := filepath.Join(repoDir, ".gokg")
	defaultRootFile := filepath.Join(dbDir, filepath.Base(telemetrypkg.DefaultFile))
	tests := []struct {
		name          string
		telemetryPath string
		wantErr       bool
	}{
		{name: "default root file allowed", telemetryPath: defaultRootFile},
		{name: "nested custom file rejected", telemetryPath: filepath.Join(dbDir, "logs", "events.jsonl"), wantErr: true},
		{name: "traversal resolving inside rejected", telemetryPath: filepath.Join(dbDir, "..", filepath.Base(dbDir), "nested.jsonl"), wantErr: true},
		{name: "db directory itself rejected", telemetryPath: dbDir, wantErr: true},
		{name: "sibling directory allowed", telemetryPath: filepath.Join(repoDir, "telemetry", "events.jsonl")},
		{name: "same-prefix sibling allowed", telemetryPath: filepath.Join(repoDir, ".gokg-telemetry", "events.jsonl")},
		{name: "parent traversal outside allowed", telemetryPath: filepath.Join(dbDir, "..", "events.jsonl")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMCPTelemetryFilePath(dbDir, tt.telemetryPath)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "must be outside")
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateMCPTelemetryFilePathResolvesSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	require.NoError(t, os.Mkdir(dbDir, 0o700))
	linkedDB := filepath.Join(root, "apparently-outside")
	if err := os.Symlink(dbDir, linkedDB); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := validateMCPTelemetryFilePath(dbDir, filepath.Join(linkedDB, "nested", "events.jsonl"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be outside")
}

func TestValidatedMCPTelemetryFilePathPinsResolvedAncestor(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real-telemetry")
	require.NoError(t, os.Mkdir(realDir, 0o700))
	linkedDir := filepath.Join(root, "telemetry-link")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cmd := newMCPTelemetryValidationTestCommand()
	require.NoError(t, cmd.ParseFlags([]string{
		"--db", filepath.Join(root, "db"),
		"--telemetry",
		"--telemetry-file", filepath.Join(linkedDir, "events.jsonl"),
	}))
	resolved, err := validatedMCPTelemetryFilePath(cmd, filepath.Join(linkedDir, "events.jsonl"))
	require.NoError(t, err)
	canonicalRealDir, err := filepath.EvalSymlinks(realDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(canonicalRealDir, "events.jsonl"), resolved)
}

func TestValidateMCPTelemetryFilePathRejectsCaseAliasOnInsensitiveFilesystem(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, ".gokg")
	require.NoError(t, os.Mkdir(dbDir, 0o700))
	caseAlias := filepath.Join(root, ".GOKG")
	dbInfo, err := os.Stat(dbDir)
	require.NoError(t, err)
	aliasInfo, err := os.Stat(caseAlias)
	if err != nil || !os.SameFile(dbInfo, aliasInfo) {
		t.Skip("test filesystem is case-sensitive")
	}

	err = validateMCPTelemetryFilePath(dbDir, filepath.Join(caseAlias, "nested", "events.jsonl"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be outside")

	err = validateMCPTelemetryFilePath(dbDir, filepath.Join(caseAlias, "Telemetry.JSONL"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only the root")
}

func TestValidateMCPTelemetryWorkspacePathsChecksEveryRepoDB(t *testing.T) {
	ws := &workspace.Workspace{
		Name: "platform",
		Dir:  t.TempDir(),
		Config: workspace.Config{Repos: map[string]string{
			"repo-a": "/repo/a",
			"repo-b": "/repo/b",
		}},
	}
	insideRepoB := filepath.Join(ws.GetRepoDBPath("repo-b"), "nested", "events.jsonl")
	err := validateMCPTelemetryWorkspacePaths(ws, insideRepoB)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repo-b")

	assert.NoError(t, validateMCPTelemetryWorkspacePaths(ws, filepath.Join(t.TempDir(), "events.jsonl")))
}

func TestMCPServerOptionsTelemetryDisabledDoesNotCreateFile(t *testing.T) {
	withWorkingDir(t, t.TempDir())
	cmd := newMCPTelemetryValidationTestCommand()

	opts, closeFn, err := mcpServerOptionsFromFlags(cmd, nil)
	require.NoError(t, err)
	require.Len(t, opts, 1)
	require.NoError(t, closeFn())
	assert.NoFileExists(t, telemetrypkg.DefaultFile)
}

func TestMCPServerOptionsTelemetryUsesRecorderOptions(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "telemetry", "events.jsonl")
	cmd := newMCPTelemetryValidationTestCommand()
	require.NoError(t, cmd.ParseFlags([]string{
		"--db", filepath.Join(tempDir, "db"),
		"--telemetry",
		"--telemetry-file", path,
		"--telemetry-max-bytes", "1024",
		"--telemetry-max-backups", "2",
	}))
	require.NoError(t, validateMCPTelemetryFlags(cmd, nil))

	opts, closeFn, err := mcpServerOptionsFromFlags(cmd, nil)
	require.NoError(t, err)
	require.Len(t, opts, 2)
	require.FileExists(t, path)
	require.NoError(t, closeFn())
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
}

func newMCPTelemetryValidationTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "mcp",
		Args:          cobra.NoArgs,
		PreRunE:       validateMCPTelemetryFlags,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	cmd.Flags().String("db", defaultDBPath, "test database path")
	addMCPTelemetryFlags(cmd)
	return cmd
}

func TestOpenWatchStorageWithRetryEventuallyOpens(t *testing.T) {
	wantErr := errors.New("Cannot acquire directory lock: resource temporarily unavailable")
	store := noopStorage{}
	attempts := 0

	got, err := openWatchStorageWithRetry(
		context.Background(),
		".gokg/",
		func(path string) (storage.Storage, error) {
			attempts++
			if attempts < 3 {
				return nil, wantErr
			}
			return store, nil
		},
		time.Second,
		time.Millisecond,
	)

	require.NoError(t, err)
	assert.Equal(t, store, got)
	assert.Equal(t, 3, attempts)
}

func TestOpenWatchStorageWithRetryStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := openWatchStorageWithRetry(
		ctx,
		".gokg/",
		func(path string) (storage.Storage, error) {
			return nil, errors.New("Cannot acquire directory lock: resource temporarily unavailable")
		},
		time.Second,
		time.Millisecond,
	)

	assert.ErrorIs(t, err, context.Canceled)
}

type noopStorage struct{}

func (noopStorage) Put(ctx context.Context, key []byte, value []byte) error {
	return nil
}

func (noopStorage) Get(ctx context.Context, key []byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (noopStorage) Iterate(ctx context.Context, fn func(key []byte, value []byte) error) error {
	return nil
}

func (noopStorage) Delete(ctx context.Context, key []byte) error {
	return nil
}

func (noopStorage) Close() error {
	return nil
}
