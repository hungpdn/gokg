package mcp

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingTelemetryRecorder struct {
	calls int
}

func TestClassifyMCPErrorUsesCodeAndBoundsMessageScan(t *testing.T) {
	assert.Equal(t, "unknown_tool", classifyMCPError(-32601, strings.Repeat("X", 4<<20)))
	assert.Equal(t, "invalid_params", classifyMCPError(-32602, strings.Repeat("X", 4<<20)))
	assert.Equal(t, "tool_error", classifyMCPError(-32000, strings.Repeat("X", maxMCPErrorClassificationBytes)+" node not found"))
	assert.Equal(t, "node_not_found", classifyMCPError(-32000, "node not found"))
}

func (r *failingTelemetryRecorder) Record(context.Context, telemetry.Event) error {
	r.calls++
	return errors.New("disk full\ninjected suffix")
}

func TestTelemetryStdioSessionsAreRandomPerServer(t *testing.T) {
	first := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(&recordingTelemetry{}))
	second := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(&recordingTelemetry{}))

	require.NotNil(t, first.telemetry)
	require.NotNil(t, second.telemetry)
	assert.NotEmpty(t, first.telemetry.defaultSessionID)
	assert.NotEqual(t, first.telemetry.defaultSessionID, second.telemetry.defaultSessionID)
}

func TestRecordToolTelemetryTreatsMCPToolErrorResultAsFailure(t *testing.T) {
	recorder := &recordingTelemetry{}
	server := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(recorder))
	res := &Response{
		JSONRPC: "2.0",
		ID:      1,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": "tool failed"}},
			"isError": true,
		},
	}
	attachToolTelemetry(res, "search_nodes", 23, 17*time.Microsecond)
	ctx := withTelemetryRequestMetadata(context.Background(), telemetryRequestMetadata{transport: "stdio"})

	server.recordToolTelemetry(ctx, res, 41, nil)

	events := recorder.Events()
	require.Len(t, events, 1)
	event := events[0]
	assert.False(t, event.Success)
	assert.Equal(t, "tool_error", event.ErrorKind)
	assert.Equal(t, int64(17), event.DurationUS)
	assert.False(t, event.DeliveryError)
}

func TestTelemetryRecorderErrorsAreObservableAndRateLimited(t *testing.T) {
	recorder := &failingTelemetryRecorder{}
	server := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(recorder))
	state := server.telemetry
	require.NotNil(t, state)
	res := &Response{JSONRPC: "2.0", ID: 1, Result: map[string]interface{}{"content": []interface{}{}}}
	attachToolTelemetry(res, "search_nodes", 10, time.Microsecond)
	ctx := withTelemetryRequestMetadata(context.Background(), telemetryRequestMetadata{transport: "stdio"})

	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	server.recordToolTelemetry(ctx, res, 20, nil)
	server.recordToolTelemetry(ctx, res, 20, nil)
	assert.Equal(t, 1, strings.Count(logs.String(), "Failed to record MCP telemetry"))
	assert.Contains(t, logs.String(), "disk full injected suffix")

	state.recordErrorMu.Lock()
	state.lastRecordErrorLog = time.Now().Add(-telemetryRecordErrorLogInterval)
	state.recordErrorMu.Unlock()
	server.recordToolTelemetry(ctx, res, 20, nil)

	assert.Equal(t, 3, recorder.calls)
	assert.Equal(t, 2, strings.Count(logs.String(), "Failed to record MCP telemetry"))
	assert.Contains(t, logs.String(), "1 similar errors suppressed")
}
