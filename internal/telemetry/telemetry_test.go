package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONLRecorderWritesReadableEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry", "events.jsonl")
	recorder, err := NewJSONLRecorder(path)
	require.NoError(t, err)

	err = recorder.Record(context.Background(), Event{
		Timestamp:     time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		SessionID:     "session-1",
		ClientName:    "codex",
		ClientVersion: "1.0.0",
		Transport:     "stdio",
		ToolName:      "search_nodes",
		Success:       true,
		DurationMS:    12,
		RequestBytes:  17,
		ResponseBytes: 33,
	})
	require.NoError(t, err)
	require.NoError(t, recorder.Close())

	events, err := ReadJSONL(path)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, 1, event.Version)
	assert.Equal(t, "session-1", event.SessionID)
	assert.Equal(t, "codex", event.ClientName)
	assert.Equal(t, "search_nodes", event.ToolName)
	assert.True(t, event.Success)
	assert.Equal(t, 5, event.EstimatedInputTokens)
	assert.Equal(t, 9, event.EstimatedOutputTokens)
}

func TestBuildReportAggregatesDeterministically(t *testing.T) {
	events := []Event{
		{
			SessionID:             "s1",
			ClientName:            "codex",
			ClientVersion:         "1",
			Transport:             "stdio",
			ToolName:              "search_nodes",
			Success:               true,
			DurationMS:            10,
			RequestBytes:          16,
			ResponseBytes:         40,
			EstimatedInputTokens:  4,
			EstimatedOutputTokens: 10,
		},
		{
			SessionID:     "s1",
			ClientName:    "codex",
			ClientVersion: "1",
			Transport:     "stdio",
			ToolName:      "search_nodes",
			Success:       false,
			ErrorCode:     -32601,
			ErrorKind:     "unknown_tool",
			DurationMS:    30,
			RequestBytes:  20,
			ResponseBytes: 24,
		},
		{
			SessionID:     "s2",
			ClientName:    "zed",
			Transport:     "http",
			ToolName:      "get_node_context",
			Success:       true,
			DurationMS:    20,
			RequestBytes:  12,
			ResponseBytes: 28,
		},
	}

	report := BuildReport(events, "events.jsonl")

	assert.Equal(t, "events.jsonl", report.Source)
	assert.Equal(t, 3, report.TotalCalls)
	assert.Equal(t, 2, report.SuccessfulCalls)
	assert.Equal(t, 1, report.FailedCalls)
	assert.InDelta(t, 1.0/3.0, report.ErrorRate, 0.0001)
	assert.Equal(t, LatencyStats{P50: 20, P95: 30, Max: 30}, report.LatencyMS)

	require.Len(t, report.Tools, 2)
	assert.Equal(t, "search_nodes", report.Tools[0].Name)
	assert.Equal(t, 2, report.Tools[0].Calls)
	assert.Equal(t, 1, report.Tools[0].FailedCalls)
	assert.Equal(t, "get_node_context", report.Tools[1].Name)

	require.Len(t, report.Clients, 2)
	assert.Equal(t, "codex@1", report.Clients[0].Name)
	assert.Equal(t, "zed", report.Clients[1].Name)
}
