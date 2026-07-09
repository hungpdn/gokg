package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPHandlerInitialize(t *testing.T) {
	server := NewServer(graph.NewGraph(nil))
	handler := server.HTTPHandler("/mcp")

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", body)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))

	var res Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, "2.0", res.JSONRPC)
	assert.Nil(t, res.Error)
	require.NotNil(t, res.Result)
}

func TestHTTPHandlerTelemetryCapturesSessionAndClient(t *testing.T) {
	recorder := &recordingTelemetry{}
	server := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(recorder))
	handler := server.HTTPHandler("/mcp")

	initBody := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"http-agent","version":"2.1"}}}`)
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", initBody)
	initReq.Header.Set("Mcp-Session-Id", "session-http-1")
	initReq.Header.Set("User-Agent", "test-agent/2.1")
	initRec := httptest.NewRecorder()
	handler.ServeHTTP(initRec, initReq)
	require.Equal(t, http.StatusOK, initRec.Code)

	callBody := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_nodes","arguments":{"query":"x"}}}`)
	callReq := httptest.NewRequest(http.MethodPost, "/mcp", callBody)
	callReq.Header.Set("Mcp-Session-Id", "session-http-1")
	callReq.Header.Set("User-Agent", "test-agent/2.1")
	callRec := httptest.NewRecorder()
	handler.ServeHTTP(callRec, callReq)
	require.Equal(t, http.StatusOK, callRec.Code)

	events := recorder.Events()
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, "session-http-1", event.SessionID)
	assert.Equal(t, "http-agent", event.ClientName)
	assert.Equal(t, "2.1", event.ClientVersion)
	assert.Equal(t, "http", event.Transport)
	assert.Equal(t, "test-agent/2.1", event.UserAgent)
	assert.Equal(t, "search_nodes", event.ToolName)
	assert.True(t, event.Success)
}

func TestHTTPHandlerRejectsNonLocalBrowserOrigin(t *testing.T) {
	server := NewServer(graph.NewGraph(nil))
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", body)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	server.HTTPHandler("/mcp").ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var res Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.NotNil(t, res.Error)
	assert.Equal(t, "Origin not allowed", res.Error.Message)
}

func TestHTTPHandlerParseError(t *testing.T) {
	server := NewServer(graph.NewGraph(nil))
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{`))
	rec := httptest.NewRecorder()

	server.HTTPHandler("/mcp").ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var res Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.NotNil(t, res.Error)
	assert.Equal(t, -32700, res.Error.Code)
}

func TestHTTPHandlerOptionsAndMethodNotAllowed(t *testing.T) {
	server := NewServer(graph.NewGraph(nil))
	handler := server.HTTPHandler("/mcp")

	optionsReq := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	optionsRec := httptest.NewRecorder()
	handler.ServeHTTP(optionsRec, optionsReq)
	assert.Equal(t, http.StatusNoContent, optionsRec.Code)
	assert.Equal(t, "POST, GET, OPTIONS", optionsRec.Header().Get("Access-Control-Allow-Methods"))

	getReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	assert.Equal(t, http.StatusMethodNotAllowed, getRec.Code)
	assert.Equal(t, "POST, OPTIONS", getRec.Header().Get("Allow"))
}

func TestHTTPHandlerHealth(t *testing.T) {
	server := NewServer(graph.NewGraph(nil))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.HTTPHandler("/mcp").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "gokg", body["server"])
}
