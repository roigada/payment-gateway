package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverPanicWritesInternalServerErrorBeforeResponseStarts(t *testing.T) {
	handler := newMiddlewareTestServer().recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "close", rec.Header().Get("Connection"))
	assert.JSONEq(t, `{"error":{"code":"internal_server_error","message":"Internal Server Error"}}`, rec.Body.String())
}

func TestRecoverPanicLogsRecoveredPanic(t *testing.T) {
	var logs bytes.Buffer
	handler := newMiddlewareTestServerWithLogger(slog.New(slog.NewJSONHandler(&logs, nil))).recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodPost, "/panic?debug=true", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("User-Agent", "test-agent")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := decodeLogEntry(t, logs.Bytes())
	assert.Equal(t, "ERROR", entry["level"])
	assert.Equal(t, "http panic recovered", entry["msg"])
	assert.Equal(t, http.MethodPost, entry["method"])
	assert.Equal(t, "/panic?debug=true", entry["uri"])
	assert.Equal(t, "192.0.2.10:1234", entry["remote_addr"])
	assert.Equal(t, "HTTP/1.1", entry["proto"])
	assert.Equal(t, "test-agent", entry["user_agent"])
	assert.Equal(t, "boom", entry["panic_value"])
	assert.Contains(t, entry["stack"], "goroutine")
}

func TestRecoverPanicDoesNotWriteErrorAfterResponseStarts(t *testing.T) {
	handler := newMiddlewareTestServer().recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"started":true}`))
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	assert.Equal(t, http.StatusAccepted, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "close", rec.Header().Get("Connection"))
	assert.Equal(t, `{"started":true}`, rec.Body.String())
}

func TestRecoverPanicRepanicsErrAbortHandler(t *testing.T) {
	handler := newMiddlewareTestServer().recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/abort", nil)

	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(rec, req)
	})
}

func TestLogRequestWritesInfoForCompletedRequests(t *testing.T) {
	var logs bytes.Buffer
	server := newMiddlewareTestServerWithLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	handler := server.logRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz?ready=true", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("User-Agent", "test-agent")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	entry := decodeLogEntry(t, logs.Bytes())
	assert.Equal(t, "INFO", entry["level"])
	assert.Equal(t, "http request", entry["msg"])
	assert.Equal(t, http.MethodGet, entry["method"])
	assert.Equal(t, "/healthz?ready=true", entry["uri"])
	assert.Equal(t, float64(http.StatusNoContent), entry["status"])
	assert.Contains(t, entry, "duration_ms")
	assert.Equal(t, "192.0.2.10:1234", entry["remote_addr"])
	assert.Equal(t, "HTTP/1.1", entry["proto"])
	assert.Equal(t, "test-agent", entry["user_agent"])
}

func TestLogRequestWritesErrorForServerErrors(t *testing.T) {
	var logs bytes.Buffer
	server := newMiddlewareTestServerWithLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	handler := server.logRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	entry := decodeLogEntry(t, logs.Bytes())
	assert.Equal(t, "ERROR", entry["level"])
	assert.Equal(t, float64(http.StatusInternalServerError), entry["status"])
}

func TestResponseRecorderKeepsFirstFinalStatus(t *testing.T) {
	rec := &responseRecorder{ResponseWriter: httptest.NewRecorder()}

	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusInternalServerError)

	assert.True(t, rec.wroteHeader)
	assert.Equal(t, http.StatusCreated, rec.status)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newMiddlewareTestServer() *Server {
	return newMiddlewareTestServerWithLogger(discardLogger())
}

func newMiddlewareTestServerWithLogger(logger *slog.Logger) *Server {
	return &Server{logger: logger}
}

func decodeLogEntry(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var entry map[string]any
	require.NoError(t, json.Unmarshal(data, &entry))
	return entry
}
