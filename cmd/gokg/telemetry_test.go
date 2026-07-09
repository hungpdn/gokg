package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	telemetrypkg "github.com/hungpdn/gokg/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelemetryStatsCommandMarkdown(t *testing.T) {
	path := writeTelemetryTestFile(t)

	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--file", path})

	require.NoError(t, cmd.Execute())
	output := stdout.String()
	assert.Contains(t, output, "GoKG MCP Telemetry")
	assert.Contains(t, output, "Calls: 1 successful, 1 failed, 2 total")
	assert.Contains(t, output, "Estimated Tokens:")
	assert.Contains(t, output, "Tools:")
	assert.Contains(t, output, "search_nodes")
	assert.Contains(t, output, "Agents:")
	assert.Contains(t, output, "codex@5")
	assert.Contains(t, output, "Sessions:")
	assert.Contains(t, output, "session-1")
}

func TestTelemetryStatsCommandJSON(t *testing.T) {
	path := writeTelemetryTestFile(t)

	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--file", path, "--json"})

	require.NoError(t, cmd.Execute())
	assert.True(t, json.Valid(stdout.Bytes()))

	var report telemetrypkg.Report
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	assert.Equal(t, path, report.Source)
	assert.Equal(t, 2, report.TotalCalls)
	assert.Equal(t, 1, report.FailedCalls)
	require.NotEmpty(t, report.Tools)
	assert.Equal(t, "search_nodes", report.Tools[0].Name)
}

func TestTelemetryStatsCommandMissingFilePrintsEmptyReport(t *testing.T) {
	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--file", filepath.Join(t.TempDir(), "missing.jsonl")})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "Calls: 0 successful, 0 failed, 0 total")
	assert.Contains(t, stdout.String(), "No telemetry events found.")
}

func writeTelemetryTestFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "telemetry.jsonl")
	recorder, err := telemetrypkg.NewJSONLRecorder(path)
	require.NoError(t, err)
	for _, event := range []telemetrypkg.Event{
		{
			SessionID:     "session-1",
			ClientName:    "codex",
			ClientVersion: "5",
			Transport:     "stdio",
			ToolName:      "search_nodes",
			Success:       true,
			DurationMS:    4,
			RequestBytes:  20,
			ResponseBytes: 40,
		},
		{
			SessionID:     "session-1",
			ClientName:    "codex",
			ClientVersion: "5",
			Transport:     "stdio",
			ToolName:      "search_nodes",
			Success:       false,
			ErrorCode:     -32601,
			ErrorKind:     "unknown_tool",
			DurationMS:    8,
			RequestBytes:  24,
			ResponseBytes: 24,
		},
	} {
		require.NoError(t, recorder.Record(context.Background(), event))
	}
	require.NoError(t, recorder.Close())
	return path
}
