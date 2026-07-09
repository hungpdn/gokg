package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultFile   = ".gokg/telemetry.jsonl"
	eventVersion  = 1
	tokenByteSize = 4
)

// Event is one append-only MCP tool-call telemetry record.
type Event struct {
	Version               int       `json:"version"`
	Timestamp             time.Time `json:"timestamp"`
	SessionID             string    `json:"session_id"`
	ClientName            string    `json:"client_name,omitempty"`
	ClientVersion         string    `json:"client_version,omitempty"`
	UserAgent             string    `json:"user_agent,omitempty"`
	Transport             string    `json:"transport"`
	ToolName              string    `json:"tool_name"`
	Success               bool      `json:"success"`
	ErrorCode             int       `json:"error_code,omitempty"`
	ErrorKind             string    `json:"error_kind,omitempty"`
	DurationMS            int64     `json:"duration_ms"`
	RequestBytes          int       `json:"request_bytes"`
	ResponseBytes         int       `json:"response_bytes"`
	EstimatedInputTokens  int       `json:"estimated_input_tokens"`
	EstimatedOutputTokens int       `json:"estimated_output_tokens"`
}

// Recorder stores telemetry events. Implementations should be safe for
// concurrent MCP HTTP requests.
type Recorder interface {
	Record(context.Context, Event) error
}

// JSONLRecorder appends one telemetry event per line.
type JSONLRecorder struct {
	mu   sync.Mutex
	file *os.File
}

func NewJSONLRecorder(path string) (*JSONLRecorder, error) {
	path = normalizePath(path)
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create telemetry directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open telemetry file: %w", err)
	}
	return &JSONLRecorder{file: file}, nil
}

func (r *JSONLRecorder) Record(ctx context.Context, event Event) error {
	if r == nil || r.file == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	event = prepareEvent(event)
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal telemetry event: %w", err)
	}
	line = append(line, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.file.Write(line); err != nil {
		return fmt.Errorf("write telemetry event: %w", err)
	}
	return nil
}

func (r *JSONLRecorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.file.Close()
	r.file = nil
	return err
}

func ReadJSONL(path string) ([]Event, error) {
	path = normalizePath(path)
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var events []Event
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("decode telemetry line %d: %w", lineNo, err)
		}
		events = append(events, prepareEvent(event))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultFile
	}
	return path
}

type Report struct {
	Source                string       `json:"source"`
	TotalCalls            int          `json:"total_calls"`
	SuccessfulCalls       int          `json:"successful_calls"`
	FailedCalls           int          `json:"failed_calls"`
	ErrorRate             float64      `json:"error_rate"`
	RequestBytes          int          `json:"request_bytes"`
	ResponseBytes         int          `json:"response_bytes"`
	EstimatedInputTokens  int          `json:"estimated_input_tokens"`
	EstimatedOutputTokens int          `json:"estimated_output_tokens"`
	LatencyMS             LatencyStats `json:"latency_ms"`
	Tools                 []GroupStats `json:"tools"`
	Clients               []GroupStats `json:"clients"`
	Sessions              []GroupStats `json:"sessions"`
	Transports            []GroupStats `json:"transports"`
}

type LatencyStats struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	Max int64 `json:"max"`
}

type GroupStats struct {
	Name                  string       `json:"name"`
	Calls                 int          `json:"calls"`
	FailedCalls           int          `json:"failed_calls"`
	ErrorRate             float64      `json:"error_rate"`
	RequestBytes          int          `json:"request_bytes"`
	ResponseBytes         int          `json:"response_bytes"`
	EstimatedInputTokens  int          `json:"estimated_input_tokens"`
	EstimatedOutputTokens int          `json:"estimated_output_tokens"`
	LatencyMS             LatencyStats `json:"latency_ms"`
}

type groupAccumulator struct {
	stats     GroupStats
	durations []int64
}

func BuildReport(events []Event, source string) Report {
	report := Report{Source: source}
	toolGroups := make(map[string]*groupAccumulator)
	clientGroups := make(map[string]*groupAccumulator)
	sessionGroups := make(map[string]*groupAccumulator)
	transportGroups := make(map[string]*groupAccumulator)
	var durations []int64

	for _, event := range events {
		event = prepareEvent(event)
		report.TotalCalls++
		if event.Success {
			report.SuccessfulCalls++
		} else {
			report.FailedCalls++
		}
		report.RequestBytes += event.RequestBytes
		report.ResponseBytes += event.ResponseBytes
		report.EstimatedInputTokens += event.EstimatedInputTokens
		report.EstimatedOutputTokens += event.EstimatedOutputTokens
		durations = append(durations, event.DurationMS)

		addGroupEvent(toolGroups, event.ToolName, event)
		addGroupEvent(clientGroups, ClientLabel(event), event)
		addGroupEvent(sessionGroups, event.SessionID, event)
		addGroupEvent(transportGroups, event.Transport, event)
	}

	report.ErrorRate = errorRate(report.FailedCalls, report.TotalCalls)
	report.LatencyMS = latencyStats(durations)
	report.Tools = finalizeGroups(toolGroups)
	report.Clients = finalizeGroups(clientGroups)
	report.Sessions = finalizeGroups(sessionGroups)
	report.Transports = finalizeGroups(transportGroups)
	return report
}

func ClientLabel(event Event) string {
	name := strings.TrimSpace(event.ClientName)
	version := strings.TrimSpace(event.ClientVersion)
	if name != "" && version != "" {
		return name + "@" + version
	}
	if name != "" {
		return name
	}
	userAgent := strings.TrimSpace(event.UserAgent)
	if userAgent != "" {
		return userAgent
	}
	return "unknown"
}

func EstimateTokensFromBytes(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + tokenByteSize - 1) / tokenByteSize
}

func prepareEvent(event Event) Event {
	if event.Version == 0 {
		event.Version = eventVersion
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.SessionID = fallbackString(event.SessionID, "unknown")
	event.Transport = fallbackString(event.Transport, "unknown")
	event.ToolName = fallbackString(event.ToolName, "unknown")
	if event.DurationMS < 0 {
		event.DurationMS = 0
	}
	if event.RequestBytes < 0 {
		event.RequestBytes = 0
	}
	if event.ResponseBytes < 0 {
		event.ResponseBytes = 0
	}
	if event.EstimatedInputTokens == 0 {
		event.EstimatedInputTokens = EstimateTokensFromBytes(event.RequestBytes)
	}
	if event.EstimatedOutputTokens == 0 {
		event.EstimatedOutputTokens = EstimateTokensFromBytes(event.ResponseBytes)
	}
	if event.Success {
		event.ErrorCode = 0
		event.ErrorKind = ""
	} else if strings.TrimSpace(event.ErrorKind) == "" {
		event.ErrorKind = "tool_error"
	}
	return event
}

func fallbackString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func addGroupEvent(groups map[string]*groupAccumulator, name string, event Event) {
	name = fallbackString(name, "unknown")
	group := groups[name]
	if group == nil {
		group = &groupAccumulator{stats: GroupStats{Name: name}}
		groups[name] = group
	}
	group.stats.Calls++
	if !event.Success {
		group.stats.FailedCalls++
	}
	group.stats.RequestBytes += event.RequestBytes
	group.stats.ResponseBytes += event.ResponseBytes
	group.stats.EstimatedInputTokens += event.EstimatedInputTokens
	group.stats.EstimatedOutputTokens += event.EstimatedOutputTokens
	group.durations = append(group.durations, event.DurationMS)
}

func finalizeGroups(groups map[string]*groupAccumulator) []GroupStats {
	out := make([]GroupStats, 0, len(groups))
	for _, group := range groups {
		group.stats.ErrorRate = errorRate(group.stats.FailedCalls, group.stats.Calls)
		group.stats.LatencyMS = latencyStats(group.durations)
		out = append(out, group.stats)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls == out[j].Calls {
			return out[i].Name < out[j].Name
		}
		return out[i].Calls > out[j].Calls
	})
	return out
}

func errorRate(errors int, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total)
}

func latencyStats(durations []int64) LatencyStats {
	if len(durations) == 0 {
		return LatencyStats{}
	}
	sorted := append([]int64(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	return LatencyStats{
		P50: percentile(sorted, 50),
		P95: percentile(sorted, 95),
		Max: sorted[len(sorted)-1],
	}
}

func percentile(sorted []int64, percent int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (percent*len(sorted) + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}
