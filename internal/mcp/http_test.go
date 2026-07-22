package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type partialErrorResponseWriter struct {
	header http.Header
	status int
	writer partialErrorWriter
}

func (w *partialErrorResponseWriter) Header() http.Header {
	return w.header
}

func (w *partialErrorResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *partialErrorResponseWriter) Write(p []byte) (int, error) {
	return w.writer.Write(p)
}

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
	require.NotEmpty(t, rec.Body.Bytes())
	assert.NotEqual(t, byte('\n'), rec.Body.Bytes()[rec.Body.Len()-1])

	var res Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, "2.0", res.JSONRPC)
	assert.Nil(t, res.Error)
	require.NotNil(t, res.Result)
}

func TestHTTPHandlerTelemetryIsStatelessAndIgnoresUntrustedMetadata(t *testing.T) {
	recorder := &recordingTelemetry{}
	server := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(recorder))
	handler := server.HTTPHandler("/mcp")

	for id, client := range []string{"http-agent-a", "http-agent-b"} {
		initBody := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"` + client + `","version":"2.1"}}}`)
		initReq := httptest.NewRequest(http.MethodPost, "/mcp", initBody)
		initRec := httptest.NewRecorder()
		handler.ServeHTTP(initRec, initReq)
		require.Equal(t, http.StatusOK, initRec.Code, "initialize request %d", id)
	}

	callBodies := []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_nodes","arguments":{"query":"without-header"}}}`,
		" \n" + `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_nodes","arguments":{"query":"forged-header"}}}` + "\n ",
	}
	responseBytes := make([]int, 0, len(callBodies))
	for i, callBody := range callBodies {
		callReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(callBody))
		if i == 1 {
			callReq.Header.Set("Mcp-Session-Id", "forged-session-secret")
			callReq.Header.Set("User-Agent", "forged-user-agent-secret")
		}
		callRec := httptest.NewRecorder()
		handler.ServeHTTP(callRec, callReq)
		require.Equal(t, http.StatusOK, callRec.Code)
		require.NotEmpty(t, callRec.Body.Bytes())
		assert.True(t, json.Valid(callRec.Body.Bytes()))
		assert.NotEqual(t, byte('\n'), callRec.Body.Bytes()[callRec.Body.Len()-1])
		responseBytes = append(responseBytes, callRec.Body.Len())
	}

	events := recorder.Events()
	require.Len(t, events, 2)
	for i, event := range events {
		assert.Empty(t, event.SessionID)
		assert.Empty(t, event.ClientName)
		assert.Empty(t, event.ClientVersion)
		assert.Empty(t, event.UserAgent)
		assert.Equal(t, "http", event.Transport)
		assert.Equal(t, "search_nodes", event.ToolName)
		assert.True(t, event.Success)
		assert.Equal(t, len(callBodies[i]), event.RequestBytes)
		assert.Equal(t, responseBytes[i], event.ResponseBytes)
		assert.False(t, event.DeliveryError)
	}
	encoded, err := json.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "forged-session-secret")
	assert.NotContains(t, string(encoded), "forged-user-agent-secret")
	assert.NotContains(t, string(encoded), "http-agent-a")
	assert.NotContains(t, string(encoded), "http-agent-b")
}

func TestHTTPHandlerTelemetryRecordsDeliveryFailure(t *testing.T) {
	recorder := &recordingTelemetry{}
	server := NewServer(graph.NewGraph(nil), WithTelemetryRecorder(recorder))
	callBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_nodes","arguments":{"query":"x"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(callBody))
	w := &partialErrorResponseWriter{
		header: make(http.Header),
		writer: partialErrorWriter{maxBytes: 11, err: io.ErrClosedPipe},
	}

	server.HTTPHandler("/mcp").ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.status)
	events := recorder.Events()
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, len(callBody), event.RequestBytes)
	assert.Equal(t, w.writer.written, event.ResponseBytes)
	assert.True(t, event.DeliveryError)
	assert.True(t, event.Success)
}

func TestHTTPHandlerTelemetryDisabledWritesUndelimitedJSON(t *testing.T) {
	server := NewServer(graph.NewGraph(nil))
	callBody := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_nodes","arguments":{"query":"x"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(callBody))
	rec := httptest.NewRecorder()

	server.HTTPHandler("/mcp").ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, server.telemetry)
	assert.True(t, json.Valid(rec.Body.Bytes()))
	require.NotEmpty(t, rec.Body.Bytes())
	assert.NotEqual(t, byte('\n'), rec.Body.Bytes()[rec.Body.Len()-1])
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
