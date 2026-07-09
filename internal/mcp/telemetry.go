package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hungpdn/gokg/internal/telemetry"
)

type telemetryState struct {
	recorder         telemetry.Recorder
	defaultSessionID string

	mu      sync.RWMutex
	clients map[string]telemetryClient
}

type telemetryClient struct {
	name    string
	version string
}

type telemetryRequestMetadata struct {
	transport string
	sessionID string
	userAgent string
}

type telemetryContextKey struct{}

func WithTelemetryRecorder(recorder telemetry.Recorder) ServerOption {
	return func(s *Server) {
		if recorder == nil {
			return
		}
		s.telemetry = &telemetryState{
			recorder:         recorder,
			defaultSessionID: newTelemetrySessionID("stdio"),
			clients:          make(map[string]telemetryClient),
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
	if s.telemetry == nil {
		return
	}
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	if name == "" && version == "" {
		return
	}

	sessionID := s.telemetrySessionID(ctx)
	s.telemetry.mu.Lock()
	defer s.telemetry.mu.Unlock()
	s.telemetry.clients[sessionID] = telemetryClient{name: name, version: version}
}

func (s *Server) recordToolTelemetry(ctx context.Context, toolName string, params json.RawMessage, res *Response, duration time.Duration) {
	if s.telemetry == nil || s.telemetry.recorder == nil {
		return
	}
	metadata := telemetryMetadataFromContext(ctx)
	sessionID := s.telemetrySessionID(ctx)
	client := s.telemetryClient(sessionID)

	responseBytes := 0
	if res != nil {
		if encoded, err := json.Marshal(res); err == nil {
			responseBytes = len(encoded)
		}
	}

	event := telemetry.Event{
		Timestamp:     time.Now().UTC(),
		SessionID:     sessionID,
		ClientName:    client.name,
		ClientVersion: client.version,
		UserAgent:     metadata.userAgent,
		Transport:     telemetryTransport(metadata),
		ToolName:      strings.TrimSpace(toolName),
		Success:       res != nil && res.Error == nil,
		DurationMS:    duration.Milliseconds(),
		RequestBytes:  len(params),
		ResponseBytes: responseBytes,
	}
	event.EstimatedInputTokens = telemetry.EstimateTokensFromBytes(event.RequestBytes)
	event.EstimatedOutputTokens = telemetry.EstimateTokensFromBytes(event.ResponseBytes)
	if res != nil && res.Error != nil {
		event.ErrorCode = res.Error.Code
		event.ErrorKind = classifyMCPError(res.Error.Message)
	}

	if err := s.telemetry.recorder.Record(context.Background(), event); err != nil {
		log.Printf("Warning: Failed to record MCP telemetry: %v", err)
	}
}

func (s *Server) telemetrySessionID(ctx context.Context) string {
	if s.telemetry == nil {
		return "unknown"
	}
	metadata := telemetryMetadataFromContext(ctx)
	if strings.TrimSpace(metadata.sessionID) != "" {
		return strings.TrimSpace(metadata.sessionID)
	}
	if metadata.transport == "http" {
		return "http-unknown-session"
	}
	return s.telemetry.defaultSessionID
}

func (s *Server) telemetryClient(sessionID string) telemetryClient {
	if s.telemetry == nil {
		return telemetryClient{}
	}
	s.telemetry.mu.RLock()
	defer s.telemetry.mu.RUnlock()
	return s.telemetry.clients[sessionID]
}

func telemetryTransport(metadata telemetryRequestMetadata) string {
	transport := strings.TrimSpace(metadata.transport)
	if transport == "" {
		return "stdio"
	}
	return transport
}

func newTelemetrySessionID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), time.Now().UnixNano())
}

func classifyMCPError(message string) string {
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
