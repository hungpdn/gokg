package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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
			if err := server.Shutdown(shutdownCtx); err != nil {
				log.Printf("HTTP MCP shutdown error: %v", err)
			}
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
	if !setHTTPHeaders(w, r) {
		writeHTTPError(w, nil, -32600, "Origin not allowed", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeHTTPError(w, nil, -32600, "MCP HTTP endpoint accepts POST requests", http.StatusMethodNotAllowed)
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			return
		}
	}()

	bodyReader := io.Reader(http.MaxBytesReader(w, r.Body, maxHTTPBodyBytes))
	var requestCounter *countingReader
	if s.telemetry != nil {
		requestCounter = &countingReader{Reader: bodyReader}
		bodyReader = requestCounter
	}
	var req Request
	decoder := json.NewDecoder(bodyReader)
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
	reqCtx := r.Context()
	if requestCounter != nil {
		req.requestBytes = requestCounter.bytes
		reqCtx = withTelemetryRequestMetadata(reqCtx, telemetryRequestMetadata{transport: "http"})
	}
	res := s.handleRequestContext(reqCtx, &req)
	if res == nil {
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		res = &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Method not found: " + req.Method}}
	}

	responseBytes, deliveryErr := writeHTTPJSON(w, http.StatusOK, res)
	if s.telemetry != nil {
		s.recordToolTelemetry(reqCtx, res, responseBytes, deliveryErr)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if !setHTTPHeaders(w, r) {
		writeHTTPError(w, nil, -32600, "Origin not allowed", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		writeHTTPError(w, nil, -32600, "Health endpoint accepts GET requests", http.StatusMethodNotAllowed)
		return
	}
	_, _ = writeHTTPJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"server": "gokg",
	})
}

func setHTTPHeaders(w http.ResponseWriter, r *http.Request) bool {
	h := w.Header()
	h.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Accept, MCP-Protocol-Version, Mcp-Session-Id")
	h.Add("Vary", "Origin")

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if !isAllowedHTTPOrigin(origin) {
		return false
	}

	h.Set("Access-Control-Allow-Origin", origin)
	return true
}

func isAllowedHTTPOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}

	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeHTTPError(w http.ResponseWriter, id interface{}, code int, message string, status int) {
	_, _ = writeHTTPJSON(w, status, &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	})
}

func writeHTTPJSON(w http.ResponseWriter, status int, value interface{}) (int, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payloadBytes, err := w.Write(data)
	if payloadBytes < 0 {
		payloadBytes = 0
	} else if payloadBytes > len(data) {
		payloadBytes = len(data)
	}
	if err == nil && payloadBytes != len(data) {
		err = io.ErrShortWrite
	}
	return payloadBytes, err
}

type countingReader struct {
	io.Reader
	bytes int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += n
	return n, err
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
