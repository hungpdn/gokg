package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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
	assert.Contains(t, output, "Latency (us, approximate")
	assert.Contains(t, output, "Delivery Failures:")
	assert.Contains(t, output, "Diagnostics:")
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
	assert.Equal(t, uint64(2), report.TotalCalls)
	assert.Equal(t, uint64(1), report.FailedCalls)
	require.NotEmpty(t, report.Tools)
	assert.Equal(t, "search_nodes", report.Tools[0].Name)
}

func TestTelemetryStatsCommandDefaultMissingFilePrintsEmptyReport(t *testing.T) {
	withWorkingDir(t, t.TempDir())

	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "Calls: 0 successful, 0 failed, 0 total")
	assert.Contains(t, stdout.String(), "No telemetry events found.")
}

func TestTelemetryStatsCommandDefaultMissingFileKeepsJSONCompatibility(t *testing.T) {
	withWorkingDir(t, t.TempDir())

	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--json"})

	require.NoError(t, cmd.Execute())
	assert.True(t, json.Valid(stdout.Bytes()))
	var report telemetrypkg.Report
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	assert.Equal(t, telemetrypkg.DefaultFile, report.Source)
	assert.Zero(t, report.TotalCalls)
}

func TestTelemetryStatsCommandExplicitMissingFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.jsonl")
	cmd := newTelemetryStatsCommand()
	cmd.SetArgs([]string{"--file", path})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestTelemetryStatsCommandExistingEmptyFilePrintsEmptyReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--file", path})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "No telemetry events found.")
}

func TestTelemetryStatsCommandRejectsInvalidCLI(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "positional argument", args: []string{"unexpected"}},
		{name: "blank explicit file", args: []string{"--file", "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTelemetryStatsCommand()
			cmd.SetArgs(tt.args)
			assert.Error(t, cmd.Execute())
		})
	}
}

func TestTelemetryReportHasDataQualityIssues(t *testing.T) {
	tests := []struct {
		name   string
		report telemetrypkg.Report
		want   bool
	}{
		{name: "clean", report: telemetrypkg.Report{}},
		{name: "delivery failure", report: telemetrypkg.Report{DeliveryFailures: 1}, want: true},
		{name: "invalid line", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{InvalidLines: 1}}, want: true},
		{name: "truncated line", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{TruncatedLines: 1}}, want: true},
		{name: "truncated label", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{TruncatedLabels: 1}}, want: true},
		{name: "redacted identity", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{RedactedIdentityFields: 1}}, want: true},
		{name: "legacy event", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{LegacyEvents: 1}}, want: true},
		{name: "unsupported version", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{UnsupportedVersions: 1}}, want: true},
		{name: "group limit overflow", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{GroupLimitOverflows: 1}}, want: true},
		{name: "overflow", report: telemetrypkg.Report{Diagnostics: telemetrypkg.ReportDiagnostics{OverflowedValues: 1}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, telemetryReportHasDataQualityIssues(tt.report))
		})
	}
}

func TestTelemetryStatsCommandStrictFailsAfterPrintingReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery-failure.jsonl")
	recorder, err := telemetrypkg.NewJSONLRecorder(path)
	require.NoError(t, err)
	require.NoError(t, recorder.Record(context.Background(), telemetrypkg.Event{
		SessionID:     "session-1",
		Transport:     "stdio",
		ToolName:      "search_nodes",
		Success:       true,
		DeliveryError: true,
	}))
	require.NoError(t, recorder.Close())

	var stdout bytes.Buffer
	cmd := newTelemetryStatsCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--file", path, "--strict"})

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delivery_failures=1")
	assert.Contains(t, stdout.String(), "Delivery Failures: 1")
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
