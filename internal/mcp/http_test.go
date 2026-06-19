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
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))

	var res Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, "2.0", res.JSONRPC)
	assert.Nil(t, res.Error)
	require.NotNil(t, res.Result)
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
