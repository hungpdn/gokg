package mcp

import (
	"context"
	"crypto/rand"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hungpdn/gokg/internal/telemetry"
)

const (
	telemetryRecordTimeout          = 100 * time.Millisecond
	telemetryRecordErrorLogInterval = time.Minute
	maxMCPErrorClassificationBytes  = 512
)

type telemetryState struct {
	recorder         telemetry.Recorder
	defaultSessionID string

	clientMu    sync.RWMutex
	stdioClient telemetryClient

	recordErrorMu             sync.Mutex
	lastRecordErrorLog        time.Time
	suppressedRecordErrorLogs uint64
}

type telemetryClient struct {
	name    string
	version string
}

type telemetryRequestMetadata struct {
	transport string
}

type telemetryContextKey struct{}

type pendingToolTelemetry struct {
	toolName     string
	requestBytes int
	duration     time.Duration
}

func WithTelemetryRecorder(recorder telemetry.Recorder) ServerOption {
	return func(s *Server) {
		if recorder == nil {
			return
		}
		s.telemetry = &telemetryState{
			recorder:         recorder,
			defaultSessionID: "stdio-" + rand.Text(),
		}
	}
}

func withTelemetryRequestMetadata(ctx context.Context, metadata telemetryRequestMetadata) context.Context {
	return context.WithValue(ctx, telemetryContextKey{}, metadata)
}

func telemetryMetadataFromContext(ctx context.Context) telemetryRequestMetadata {
	metadata, _ := ctx.Value(telemetryContextKey{}).(telemetryRequestMetadata)
	return metadata
}

func (s *Server) rememberTelemetryClient(ctx context.Context, name string, version string) {
	state := s.telemetry
	if state == nil || telemetryTransport(telemetryMetadataFromContext(ctx)) == "http" {
		return
	}
	name = strings.Clone(telemetry.SanitizeOptionalLabel(name))
	version = strings.Clone(telemetry.SanitizeOptionalLabel(version))
	if name == "" && version == "" {
		return
	}

	state.clientMu.Lock()
	state.stdioClient = telemetryClient{name: name, version: version}
	state.clientMu.Unlock()
}

func attachToolTelemetry(res *Response, toolName string, requestBytes int, duration time.Duration) {
	if res == nil {
		return
	}
	if requestBytes < 0 {
		requestBytes = 0
	}
	if duration < 0 {
		duration = 0
	}
	res.telemetry = &pendingToolTelemetry{
		toolName:     strings.Clone(telemetry.SanitizeLabel(toolName, "unknown")),
		requestBytes: requestBytes,
		duration:     duration,
	}
}

func (s *Server) recordToolTelemetry(ctx context.Context, res *Response, responseBytes int, deliveryErr error) {
	state := s.telemetry
	if state == nil || state.recorder == nil || res == nil || res.telemetry == nil {
		return
	}
	if responseBytes < 0 {
		responseBytes = 0
	}
	pending := res.telemetry
	metadata := telemetryMetadataFromContext(ctx)
	transport := telemetryTransport(metadata)
	sessionID, client := state.telemetryIdentity(transport)
	success := res.Error == nil && !toolResultIsError(res.Result)

	event := telemetry.Event{
		Timestamp:     time.Now().UTC(),
		SessionID:     sessionID,
		ClientName:    client.name,
		ClientVersion: client.version,
		Transport:     transport,
		ToolName:      pending.toolName,
		Success:       success,
		DurationUS:    pending.duration.Microseconds(),
		RequestBytes:  pending.requestBytes,
		ResponseBytes: responseBytes,
		DeliveryError: deliveryErr != nil,
	}
	event.EstimatedInputTokens = telemetry.EstimateTokensFromBytes(event.RequestBytes)
	event.EstimatedOutputTokens = telemetry.EstimateTokensFromBytes(event.ResponseBytes)
	if res.Error != nil {
		event.ErrorCode = res.Error.Code
		event.ErrorKind = classifyMCPError(res.Error.Code, res.Error.Message)
	} else if !success {
		event.ErrorKind = "tool_error"
	}

	recordCtx, cancel := context.WithTimeout(context.Background(), telemetryRecordTimeout)
	defer cancel()
	if err := state.recorder.Record(recordCtx, event); err != nil {
		s.logTelemetryRecordError(err)
	}
}

func (s *telemetryState) telemetryIdentity(transport string) (string, telemetryClient) {
	if s == nil || transport == "http" {
		return "", telemetryClient{}
	}
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.defaultSessionID, s.stdioClient
}

func (s *Server) logTelemetryRecordError(err error) {
	state := s.telemetry
	if state == nil || err == nil {
		return
	}

	now := time.Now()
	state.recordErrorMu.Lock()
	if !state.lastRecordErrorLog.IsZero() &&
		!now.Before(state.lastRecordErrorLog) &&
		now.Sub(state.lastRecordErrorLog) < telemetryRecordErrorLogInterval {
		state.suppressedRecordErrorLogs++
		state.recordErrorMu.Unlock()
		return
	}
	suppressed := state.suppressedRecordErrorLogs
	state.suppressedRecordErrorLogs = 0
	state.lastRecordErrorLog = now
	state.recordErrorMu.Unlock()

	message := telemetry.SanitizeLabel(err.Error(), "unknown telemetry recorder error")
	if suppressed > 0 {
		log.Printf("Warning: Failed to record MCP telemetry: %s (%d similar errors suppressed)", message, suppressed)
		return
	}
	log.Printf("Warning: Failed to record MCP telemetry: %s", message)
}

func telemetryTransport(metadata telemetryRequestMetadata) string {
	if metadata.transport == "http" {
		return "http"
	}
	return "stdio"
}

func toolResultIsError(result interface{}) bool {
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return false
	}
	isError, _ := resultMap["isError"].(bool)
	return isError
}

func classifyMCPError(code int, message string) string {
	switch code {
	case -32601:
		return "unknown_tool"
	case -32602:
		return "invalid_params"
	}
	if len(message) > maxMCPErrorClassificationBytes {
		message = message[:maxMCPErrorClassificationBytes]
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unknown tool"):
		return "unknown_tool"
	case strings.Contains(lower, "node not found"):
		return "node_not_found"
	case strings.Contains(lower, "invalid params"):
		return "invalid_params"
	case strings.Contains(lower, "requires limit"):
		return "guardrail"
	case strings.Contains(lower, "unavailable"):
		return "unavailable"
	default:
		return "tool_error"
	}
}
