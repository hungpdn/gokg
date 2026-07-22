package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	lockDir, err := os.MkdirTemp("", "gokg-telemetry-test-locks-")
	if err != nil {
		panic(err)
	}
	restore := SetLockDirectoryForTesting(lockDir)
	code := m.Run()
	restore()
	_ = os.RemoveAll(lockDir)
	os.Exit(code)
}

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

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	event, _, err := decodeEventStrict(bytes.TrimSpace(data))
	require.NoError(t, err)
	assert.Equal(t, 2, event.Version)
	assert.Equal(t, "session-1", event.SessionID)
	assert.Equal(t, "codex", event.ClientName)
	assert.Equal(t, "search_nodes", event.ToolName)
	assert.True(t, event.Success)
	assert.Equal(t, 5, event.EstimatedInputTokens)
	assert.Equal(t, 9, event.EstimatedOutputTokens)
}

func TestNilJSONLRecorderRejectsRecord(t *testing.T) {
	var recorder *JSONLRecorder
	require.ErrorIs(t, recorder.Record(context.Background(), telemetryTestEvent()), ErrRecorderClosed)
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
	assert.Equal(t, uint64(3), report.TotalCalls)
	assert.Equal(t, uint64(2), report.SuccessfulCalls)
	assert.Equal(t, uint64(1), report.FailedCalls)
	assert.InDelta(t, 1.0/3.0, report.ErrorRate, 0.0001)
	assert.Equal(t, LatencyStats{P50: 20, P95: 30, Max: 30}, report.LatencyMS)

	require.Len(t, report.Tools, 2)
	assert.Equal(t, "search_nodes", report.Tools[0].Name)
	assert.Equal(t, uint64(2), report.Tools[0].Calls)
	assert.Equal(t, uint64(1), report.Tools[0].FailedCalls)
	assert.Equal(t, "get_node_context", report.Tools[1].Name)

	require.Len(t, report.Clients, 1)
	assert.Equal(t, "codex@1", report.Clients[0].Name)
}

func TestBuildReportFromReaderStreamsAndSanitizesLabels(t *testing.T) {
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	require.NoError(t, encoder.Encode(Event{
		Version:               eventVersion,
		Timestamp:             time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		SessionID:             " session\none ",
		ClientName:            strings.Repeat("x", MaxLabelRunes+10),
		ClientVersion:         "v1\t",
		Transport:             "stdio",
		ToolName:              "search_nodes\r",
		Success:               true,
		DurationUS:            12_000,
		RequestBytes:          8,
		ResponseBytes:         16,
		EstimatedInputTokens:  2,
		EstimatedOutputTokens: 4,
	}))

	report, err := BuildReportFromReader(&input, "inline")
	require.NoError(t, err)
	require.Equal(t, uint64(1), report.TotalCalls)
	require.Len(t, report.Sessions, 1)
	assert.Equal(t, "session one", report.Sessions[0].Name)
	require.Len(t, report.Tools, 1)
	assert.Equal(t, "search_nodes", report.Tools[0].Name)
	require.Len(t, report.Clients, 1)
	assert.NotContains(t, report.Clients[0].Name, "\t")
	assert.LessOrEqual(t, len([]rune(report.Clients[0].Name)), MaxLabelRunes+len("...@v1"))
	assert.Equal(t, uint64(1), report.Diagnostics.TruncatedLabels)
}

func TestSanitizeOptionalLabelRemovesTerminalControlAndBidiRunes(t *testing.T) {
	got := SanitizeOptionalLabel("safe\u0085\u202eevil\u2066name")
	assert.Equal(t, "safe evil name", got)
	assert.NotContains(t, got, "\u0085")
	assert.NotContains(t, got, "\u202e")
	assert.NotContains(t, got, "\u2066")
}

func TestAsyncRecorderBackpressureAndStats(t *testing.T) {
	release := make(chan struct{})
	destination := newTestRecorder(release, nil)
	recorder := NewAsyncRecorder(destination, 1)
	event := telemetryTestEvent()

	require.NoError(t, recorder.Record(context.Background(), event))
	<-destination.started
	require.NoError(t, recorder.Record(context.Background(), event))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, recorder.Record(ctx, event), context.Canceled)
	close(release)
	require.NoError(t, recorder.Shutdown(context.Background()))

	assert.Equal(t, AsyncStats{Accepted: 2, Written: 2, Rejected: 1}, recorder.Stats())
}

func TestAsyncRecorderWriteFailureIsObservable(t *testing.T) {
	sentinel := errors.New("disk full")
	destination := newTestRecorder(nil, sentinel)
	recorder := NewAsyncRecorder(destination, 1)
	require.NoError(t, recorder.Record(context.Background(), telemetryTestEvent()))
	select {
	case <-recorder.stop:
	case <-time.After(2 * time.Second):
		t.Fatal("writer failure did not stop the recorder")
	}
	require.ErrorIs(t, recorder.Record(context.Background(), telemetryTestEvent()), sentinel)
	require.ErrorIs(t, recorder.Shutdown(context.Background()), sentinel)
	assert.Equal(t, AsyncStats{Accepted: 1, Failed: 1, Rejected: 1}, recorder.Stats())
}

func TestAsyncRecorderShutdownHonorsContextAndCanFinishLater(t *testing.T) {
	release := make(chan struct{})
	destination := newTestRecorder(release, nil)
	recorder := NewAsyncRecorder(destination, 1)
	require.NoError(t, recorder.Record(context.Background(), telemetryTestEvent()))
	<-destination.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, recorder.Shutdown(ctx), context.Canceled)
	close(release)
	require.NoError(t, recorder.Shutdown(context.Background()))
	assert.Equal(t, uint64(1), recorder.Stats().Written)
}

func TestAsyncRecorderSanitizesBeforeQueue(t *testing.T) {
	release := make(chan struct{})
	destination := newTestRecorder(release, nil)
	recorder := NewAsyncRecorder(destination, 1)
	require.NoError(t, recorder.Record(context.Background(), telemetryTestEvent()))
	<-destination.started

	event := telemetryTestEvent()
	event.UserAgent = strings.Repeat("x", 4<<20)
	require.NoError(t, recorder.Record(context.Background(), event))
	queued := <-recorder.queue
	assert.LessOrEqual(t, len([]rune(queued.UserAgent)), MaxLabelRunes+len("..."))
	recorder.queue <- queued
	close(release)
	require.NoError(t, recorder.Shutdown(context.Background()))
}

func TestAsyncRecorderRecordRacingShutdownIsAccounted(t *testing.T) {
	destination := newTestRecorder(nil, nil)
	recorder := NewAsyncRecorder(destination, 64)
	start := make(chan struct{})
	results := make(chan error, 64)
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- recorder.Record(context.Background(), telemetryTestEvent())
		}()
	}
	close(start)
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- recorder.Shutdown(context.Background()) }()
	workers.Wait()
	close(results)
	require.NoError(t, <-shutdownDone)
	var accepted uint64
	for err := range results {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, ErrRecorderClosed)
		}
	}
	stats := recorder.Stats()
	assert.Equal(t, accepted, stats.Accepted)
	assert.Equal(t, stats.Accepted, stats.Written+stats.Failed)
}

func TestBuildReportLenientDiagnosticsAndV1Compatibility(t *testing.T) {
	timestamp := "2026-07-09T10:00:00Z"
	input := fmt.Sprintf("{\"version\":1,\"timestamp\":%q,\"session_id\":\"legacy-session\",\"transport\":\"stdio\",\"tool_name\":\"search_nodes\",\"success\":true,\"duration_ms\":12,\"request_bytes\":4,\"response_bytes\":8,\"estimated_input_tokens\":1,\"estimated_output_tokens\":2}\n", timestamp)
	input += "{malformed}\n"
	input += fmt.Sprintf("{\"version\":99,\"timestamp\":%q,\"duration_us\":1}\n", timestamp)
	input += strings.Repeat("x", MaxJSONLLineBytes+1) + "\n"
	input += fmt.Sprintf("{\"version\":2,\"timestamp\":%q,\"transport\":\"http\",\"tool_name\":\"x\",\"duration_us\":1}", timestamp)

	report, err := BuildReportFromReader(strings.NewReader(input), "inline")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), report.TotalCalls)
	assert.Equal(t, int64(12_000), report.LatencyUS.Max)
	assert.Equal(t, uint64(1), report.Diagnostics.LegacyEvents)
	assert.Equal(t, uint64(4), report.Diagnostics.InvalidLines)
	assert.Equal(t, uint64(1), report.Diagnostics.UnsupportedVersions)
	assert.Equal(t, uint64(2), report.Diagnostics.TruncatedLines)
}

func TestBuildReportRedactsLegacyHTTPIdentity(t *testing.T) {
	input := `{"version":1,"timestamp":"2026-07-09T10:00:00Z","session_id":"secret-session","client_name":"secret-client","client_version":"1","user_agent":"secret-agent","transport":"http","tool_name":"search_nodes","success":true,"duration_ms":1,"request_bytes":4,"response_bytes":8,"estimated_input_tokens":1,"estimated_output_tokens":2}` + "\n"

	report, err := BuildReportFromReader(strings.NewReader(input), "inline")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), report.TotalCalls)
	assert.Empty(t, report.Clients)
	assert.Empty(t, report.Sessions)
	assert.Equal(t, uint64(1), report.Diagnostics.LegacyEvents)
	assert.Equal(t, uint64(4), report.Diagnostics.RedactedIdentityFields)
}

func TestDecodeEventStrictRejectsIncompleteAndInconsistentEvents(t *testing.T) {
	valid := `{"version":2,"timestamp":"2026-07-09T10:00:00Z","session_id":"session","transport":"stdio","tool_name":"search_nodes","success":true,"duration_us":1,"request_bytes":4,"response_bytes":8,"estimated_input_tokens":1,"estimated_output_tokens":2}`
	tests := []struct {
		name string
		line string
	}{
		{name: "missing success", line: strings.Replace(valid, `,"success":true`, "", 1)},
		{name: "inconsistent tokens", line: strings.Replace(valid, `"estimated_input_tokens":1`, `"estimated_input_tokens":2`, 1)},
		{name: "invalid transport", line: strings.Replace(valid, `"transport":"stdio"`, `"transport":"tcp"`, 1)},
		{name: "success with error", line: strings.Replace(valid, `"duration_us":1`, `"error_kind":"tool_error","duration_us":1`, 1)},
		{name: "failure without kind", line: strings.Replace(valid, `"success":true`, `"success":false`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeEventStrict([]byte(tt.line))
			require.Error(t, err)
		})
	}
}

func TestNormalizeEventForWriteProducesStrictlyDecodableSchema(t *testing.T) {
	tests := []struct {
		name  string
		event Event
	}{
		{name: "stdio", event: telemetryTestEvent()},
		{name: "http identity is scrubbed", event: Event{
			Timestamp: time.Now(), SessionID: "untrusted", ClientName: "client", UserAgent: "agent",
			Transport: "HTTP", ToolName: "search_nodes", Success: true, RequestBytes: 4, ResponseBytes: 8,
		}},
		{name: "failed tool", event: Event{
			Timestamp: time.Now(), SessionID: "session", Transport: "stdio", ToolName: "search_nodes",
			Success: false, ErrorCode: -32000, ErrorKind: "tool_error",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, _, err := normalizeEventForWrite(tt.event)
			require.NoError(t, err)
			line, err := json.Marshal(prepared)
			require.NoError(t, err)
			decoded, _, err := decodeEventStrict(line)
			require.NoError(t, err)
			assert.Equal(t, prepared, decoded)
		})
	}

	invalid := telemetryTestEvent()
	invalid.Transport = "tcp"
	_, _, err := normalizeEventForWrite(invalid)
	require.Error(t, err)
	invalid = telemetryTestEvent()
	invalid.SessionID = ""
	_, _, err = normalizeEventForWrite(invalid)
	require.Error(t, err)
}

func TestReportLimitsAggregateOverflowAndSkipAnonymousGroups(t *testing.T) {
	builder := NewReportBuilderWithLimits("inline", ReportLimits{Tools: 2, Clients: 2, Sessions: 2, Transports: 2})
	for index := range 5 {
		event := telemetryTestEvent()
		event.ToolName = fmt.Sprintf("tool-%d", index)
		event.SessionID = ""
		event.ClientName = ""
		event.UserAgent = ""
		event.Transport = "http"
		event.DeliveryError = index%2 == 0
		builder.Add(event)
	}
	report := builder.Report()
	assert.Equal(t, uint64(5), report.TotalCalls)
	assert.Equal(t, uint64(3), report.DeliveryFailures)
	assert.Empty(t, report.Clients)
	assert.Empty(t, report.Sessions)
	require.Len(t, report.Tools, 3)
	var overflow *GroupStats
	for index := range report.Tools {
		if report.Tools[index].Overflow {
			overflow = &report.Tools[index]
		}
	}
	require.NotNil(t, overflow)
	assert.Equal(t, uint64(3), overflow.Calls)
	assert.Equal(t, uint64(1), report.Diagnostics.GroupLimitOverflows)
}

func TestReportBuilderDefaultLimitsBoundHighCardinality(t *testing.T) {
	const eventCount = 10_000
	limits := DefaultReportLimits()
	builder := NewReportBuilder("high-cardinality")
	for index := range eventCount {
		event := telemetryTestEvent()
		event.ToolName = fmt.Sprintf("tool-%d", index)
		event.ClientName = fmt.Sprintf("client-%d", index)
		event.ClientVersion = ""
		event.SessionID = fmt.Sprintf("session-%d", index)
		builder.Add(event)
	}

	assert.Len(t, builder.toolGroups.groups, limits.Tools)
	assert.Len(t, builder.clientGroups.groups, limits.Clients)
	assert.Len(t, builder.sessionGroups.groups, limits.Sessions)
	assert.Len(t, builder.transportGroups.groups, 1)
	require.NotNil(t, builder.toolGroups.overflow)
	require.NotNil(t, builder.clientGroups.overflow)
	require.NotNil(t, builder.sessionGroups.overflow)

	report := builder.Report()
	assert.Equal(t, uint64(eventCount), report.TotalCalls)
	assert.Len(t, report.Tools, limits.Tools+1)
	assert.Len(t, report.Clients, limits.Clients+1)
	assert.Len(t, report.Sessions, limits.Sessions+1)
	assert.Len(t, report.Transports, 1)
	assert.Equal(t, uint64(3), report.Diagnostics.GroupLimitOverflows)
	assert.Equal(t, uint64(eventCount-limits.Tools), overflowCalls(report.Tools))
	assert.Equal(t, uint64(eventCount-limits.Clients), overflowCalls(report.Clients))
	assert.Equal(t, uint64(eventCount-limits.Sessions), overflowCalls(report.Sessions))
}

func overflowCalls(groups []GroupStats) uint64 {
	for _, group := range groups {
		if group.Overflow {
			return group.Calls
		}
	}
	return 0
}

func TestJSONLRecorderRotatesAndReportReadsOldestToActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	event := telemetryTestEvent()
	prepared, _, err := normalizeEventForWrite(event)
	require.NoError(t, err)
	encoded, err := json.Marshal(prepared)
	require.NoError(t, err)

	recorder, err := NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: path, MaxFileBytes: int64(2 * (len(encoded) + 1)), MaxBackups: 2,
	})
	require.NoError(t, err)
	for range 7 {
		require.NoError(t, recorder.Record(context.Background(), event))
	}
	require.NoError(t, recorder.Close())
	assert.FileExists(t, path)
	assert.FileExists(t, path+".1")
	assert.FileExists(t, path+".2")
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	report, err := BuildReportFromJSONL(path)
	require.NoError(t, err)
	assert.Equal(t, uint64(5), report.TotalCalls)
}

func TestJSONLRecorderPrunesBackupsWhenRetentionDecreases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for index := 1; index <= 4; index++ {
		require.NoError(t, os.WriteFile(fmt.Sprintf("%s.%d", path, index), nil, 0o600))
	}

	recorder, err := NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: path, MaxFileBytes: 1024, MaxBackups: 2,
	})
	require.NoError(t, err)
	require.NoError(t, recorder.Close())
	assert.FileExists(t, path+".1")
	assert.FileExists(t, path+".2")
	assert.NoFileExists(t, path+".3")
	assert.NoFileExists(t, path+".4")

	recorder, err = NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: path, MaxFileBytes: 1024, MaxBackups: 0,
	})
	require.NoError(t, err)
	require.NoError(t, recorder.Close())
	assert.NoFileExists(t, path+".1")
	assert.NoFileExists(t, path+".2")
}

func TestJSONLRecorderDoesNotPartiallyPruneWhenValidationFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.NoError(t, os.WriteFile(path+".3", []byte("keep-three"), 0o600))
	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	if err := os.Symlink(target, path+".4"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: path, MaxFileBytes: 1024, MaxBackups: 2,
	})
	require.Error(t, err)
	assert.FileExists(t, path+".3")
	info, statErr := os.Lstat(path + ".4")
	require.NoError(t, statErr)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestJSONLRecorderDoesNotPruneBeforeActiveFileValidation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, os.WriteFile(path+".3", []byte("keep"), 0o600))

	_, err := NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: path, MaxFileBytes: 1024, MaxBackups: 2,
	})
	require.Error(t, err)
	assert.FileExists(t, path+".3")
}

func TestJSONLRecorderRejectsSecondProcessWriterForSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	first, err := NewJSONLRecorder(path)
	require.NoError(t, err)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	second, err := NewJSONLRecorder(path)
	require.Error(t, err)
	assert.Nil(t, second)
	assert.Contains(t, err.Error(), "distinct --telemetry-file")

	require.NoError(t, first.Close())
	third, err := NewJSONLRecorder(path)
	require.NoError(t, err)
	require.NoError(t, third.Close())
}

func TestJSONLRecorderLockDoesNotCaseFoldOnSensitiveFilesystem(t *testing.T) {
	root := t.TempDir()
	upperDir := filepath.Join(root, "Series")
	lowerDir := filepath.Join(root, "series")
	require.NoError(t, os.Mkdir(upperDir, 0o700))
	if err := os.Mkdir(lowerDir, 0o700); err != nil {
		t.Skip("test filesystem is case-insensitive")
	}
	upperInfo, err := os.Stat(upperDir)
	require.NoError(t, err)
	lowerInfo, err := os.Stat(lowerDir)
	require.NoError(t, err)
	if os.SameFile(upperInfo, lowerInfo) {
		t.Skip("test filesystem is case-insensitive")
	}

	upper, err := NewJSONLRecorder(filepath.Join(upperDir, "events.jsonl"))
	require.NoError(t, err)
	defer func() { require.NoError(t, upper.Close()) }()
	lower, err := NewJSONLRecorder(filepath.Join(lowerDir, "events.jsonl"))
	require.NoError(t, err)
	require.NoError(t, lower.Close())
}

func TestTelemetrySeriesHandlesCaseAliasesOnInsensitiveFilesystem(t *testing.T) {
	dir := t.TempDir()
	if !telemetryFilesystemIsCaseInsensitive(dir) {
		t.Skip("test filesystem is case-sensitive")
	}
	path := filepath.Join(dir, "events.jsonl")
	recorder, err := NewJSONLRecorder(path)
	require.NoError(t, err)
	require.NoError(t, recorder.Record(context.Background(), telemetryTestEvent()))
	require.NoError(t, recorder.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path+".1", data, 0o600))
	require.NoError(t, os.WriteFile(path+".3", data, 0o600))

	alias := filepath.Join(dir, "Events.JSONL")
	report, err := BuildReportFromJSONL(alias)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), report.TotalCalls)

	recorder, err = NewJSONLRecorderWithOptions(JSONLRecorderOptions{
		Path: alias, MaxFileBytes: 1024, MaxBackups: 2,
	})
	require.NoError(t, err)
	require.NoError(t, recorder.Close())
	assert.NoFileExists(t, path+".3")
	assert.FileExists(t, path+".1")
}

func TestBuildReportReadsBackupsWhenActiveSegmentIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	event, _, err := normalizeEventForWrite(telemetryTestEvent())
	require.NoError(t, err)
	line, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path+".1", append(line, '\n'), 0o600))

	report, err := BuildReportFromJSONL(path)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), report.TotalCalls)
}

func TestJSONLRecorderRejectsSymlinkFinalPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	link := filepath.Join(dir, "events.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := NewJSONLRecorder(link)
	require.Error(t, err)
}

func TestJSONLRecorderRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(realDir, 0o700))
	linkDir := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := NewJSONLRecorder(filepath.Join(linkDir, "events.jsonl"))
	require.Error(t, err)
}

func BenchmarkReportBuilderHighCardinality(b *testing.B) {
	events := make([]Event, 10_000)
	for index := range events {
		events[index] = telemetryTestEvent()
		events[index].SessionID = fmt.Sprintf("session-%d", index)
		events[index].DurationUS = int64(index + 1)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		builder := NewReportBuilder("benchmark")
		for _, event := range events {
			builder.Add(event)
		}
		_ = builder.Report()
	}
}

func BenchmarkAsyncRecorderRecord(b *testing.B) {
	recorder := NewAsyncRecorder(discardRecorder{}, 1024)
	event := telemetryTestEvent()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := recorder.Record(context.Background(), event); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := recorder.Shutdown(context.Background()); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkAsyncRecorderRecordParallel(b *testing.B) {
	recorder := NewAsyncRecorder(discardRecorder{}, 4096)
	event := telemetryTestEvent()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := recorder.Record(context.Background(), event); err != nil {
				b.Error(err)
				return
			}
		}
	})
	b.StopTimer()
	if err := recorder.Shutdown(context.Background()); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkBuildReportFromReader(b *testing.B) {
	for _, highCardinality := range []bool{false, true} {
		name := "low_cardinality"
		if highCardinality {
			name = "high_cardinality"
		}
		b.Run(name, func(b *testing.B) {
			data := benchmarkTelemetryJSONL(b, 10_000, highCardinality)
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for range b.N {
				if _, err := BuildReportFromReader(bytes.NewReader(data), "benchmark"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkTelemetryJSONL(b *testing.B, count int, highCardinality bool) []byte {
	b.Helper()
	var data bytes.Buffer
	for index := range count {
		event := telemetryTestEvent()
		if highCardinality {
			event.SessionID = fmt.Sprintf("session-%d", index)
			event.ToolName = fmt.Sprintf("tool-%d", index)
		}
		prepared, _, err := normalizeEventForWrite(event)
		if err != nil {
			b.Fatal(err)
		}
		line, err := json.Marshal(prepared)
		if err != nil {
			b.Fatal(err)
		}
		data.Write(line)
		data.WriteByte('\n')
	}
	return data.Bytes()
}

func telemetryTestEvent() Event {
	return Event{
		Timestamp: time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		SessionID: "session-1", ClientName: "codex", ClientVersion: "1",
		Transport: "stdio", ToolName: "search_nodes", Success: true,
		DurationUS: 12_000, RequestBytes: 16, ResponseBytes: 32,
	}
}

type testRecorder struct {
	mu          sync.Mutex
	events      []Event
	block       <-chan struct{}
	recordErr   error
	started     chan struct{}
	startedOnce sync.Once
}

type discardRecorder struct{}

func (discardRecorder) Record(context.Context, Event) error { return nil }

func newTestRecorder(block <-chan struct{}, recordErr error) *testRecorder {
	return &testRecorder{block: block, recordErr: recordErr, started: make(chan struct{})}
}

func (r *testRecorder) Record(ctx context.Context, event Event) error {
	r.startedOnce.Do(func() { close(r.started) })
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.recordErr != nil {
		return r.recordErr
	}
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return nil
}

func (r *testRecorder) Close() error { return nil }
