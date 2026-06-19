package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultHTTPPath     = "/mcp"
	maxHTTPBodyBytes    = 4 << 20
	httpShutdownTimeout = 5 * time.Second
	httpReadTimeout     = 30 * time.Second
	httpWriteTimeout    = 2 * time.Minute
	httpIdleTimeout     = 2 * time.Minute
)

// StartHTTP serves the MCP JSON-RPC endpoint over HTTP.
func (s *Server) StartHTTP(ctx context.Context, addr string, path string) error {
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           s.HTTPHandler(path),
		ReadHeaderTimeout: httpShutdownTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-done:
		}
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// HTTPHandler returns an HTTP handler for the MCP endpoint and /healthz.
func (s *Server) HTTPHandler(path string) http.Handler {
	mux := http.NewServeMux()
	rpcPath := normalizeHTTPPath(path)
	mux.HandleFunc(rpcPath, s.handleHTTPRPC)
	if rpcPath != "/healthz" {
		mux.HandleFunc("/healthz", handleHealth)
	}
	return mux
}

func (s *Server) handleHTTPRPC(w http.ResponseWriter, r *http.Request) {
	setHTTPHeaders(w)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeHTTPError(w, nil, -32600, "MCP HTTP endpoint accepts POST requests", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	var req Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHTTPBodyBytes))
	if err := decoder.Decode(&req); err != nil {
		writeHTTPError(w, nil, -32700, "Parse error", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeHTTPError(w, req.ID, -32700, "Parse error", http.StatusBadRequest)
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		writeHTTPError(w, req.ID, -32600, "Invalid Request", http.StatusBadRequest)
		return
	}

	res := s.handleRequest(&req)
	if res == nil {
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		res = &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Method not found: " + req.Method}}
	}

	writeHTTPJSON(w, http.StatusOK, res)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	setHTTPHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		writeHTTPError(w, nil, -32600, "Health endpoint accepts GET requests", http.StatusMethodNotAllowed)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"server": "gokg",
	})
}

func setHTTPHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Accept, MCP-Protocol-Version, Mcp-Session-Id")
}

func writeHTTPError(w http.ResponseWriter, id interface{}, code int, message string, status int) {
	writeHTTPJSON(w, status, &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	})
}

func writeHTTPJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func normalizeHTTPPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return defaultHTTPPath
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}
