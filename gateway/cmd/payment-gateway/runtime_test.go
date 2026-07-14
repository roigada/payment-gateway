package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeUntilShutdownDrainsStartedRequestAndChangesAvailability(t *testing.T) {
	listener := newTestListener(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	requestContextCanceled := make(chan struct{})
	readiness := newShutdownReadiness(readinessCheckerFunc(func(ctx context.Context) error {
		close(started)
		select {
		case <-finish:
			return nil
		case <-ctx.Done():
			close(requestContextCanceled)
			return ctx.Err()
		}
	}))
	logs := &bytes.Buffer{}
	handler := httpapi.NewHandler(nil, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, nil, testRuntimeHandlerOptions())
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdown(listener, &http.Server{Handler: handler}, readiness, time.Second, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
	}()
	type requestResult struct {
		response *http.Response
		err      error
	}
	requestDone := make(chan requestResult, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String() + "/readyz")
		requestDone <- requestResult{response: response, err: err}
	}()
	requireReceive(t, started)
	shutdownSignals <- syscall.SIGTERM
	require.Eventually(t, func() bool {
		return readiness.draining.Load()
	}, time.Second, time.Millisecond)
	assertChannelNotClosed(t, requestContextCanceled)
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusNoContent, health.Code)
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, ready.Code)
	close(finish)
	completedRequest := requireReceive(t, requestDone)
	require.NoError(t, completedRequest.err)
	defer completedRequest.response.Body.Close()
	assert.Equal(t, http.StatusNoContent, completedRequest.response.StatusCode)
	require.NoError(t, <-result)
	assert.Contains(t, logs.String(), "payment-gateway shutdown signal received")
	assert.Contains(t, logs.String(), "payment-gateway shutdown drain started")
	assert.Contains(t, logs.String(), "payment-gateway shutdown completed")
}

func TestServeUntilShutdownForceClosesRequestsAfterDrainDeadline(t *testing.T) {
	listener := newTestListener(t)
	started := make(chan struct{})
	requestEnded := make(chan struct{})
	readiness := newShutdownReadiness(readinessCheckerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(requestEnded)
		return ctx.Err()
	}))
	logs := &bytes.Buffer{}
	handler := httpapi.NewHandler(nil, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, nil, testRuntimeHandlerOptions())
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdown(listener, &http.Server{Handler: handler}, readiness, 10*time.Millisecond, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
	}()
	go func() {
		response, _ := http.Get("http://" + listener.Addr().String() + "/readyz")
		if response != nil {
			response.Body.Close()
		}
	}()
	requireReceive(t, started)
	shutdownSignals <- syscall.SIGINT
	requireReceive(t, requestEnded)
	require.NoError(t, <-result)
	assert.Contains(t, logs.String(), "payment-gateway shutdown drain timed out")
	assert.Contains(t, logs.String(), "payment-gateway shutdown forced connections closed")
}

func TestServeUntilShutdownSecondSignalForceClosesRequestsBeforeDrainDeadline(t *testing.T) {
	listener := newTestListener(t)
	started := make(chan struct{})
	requestEnded := make(chan struct{})
	readiness := newShutdownReadiness(readinessCheckerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(requestEnded)
		return ctx.Err()
	}))
	logs := &bytes.Buffer{}
	handler := httpapi.NewHandler(nil, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, nil, testRuntimeHandlerOptions())
	shutdownSignals := make(chan os.Signal, 2)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdown(listener, &http.Server{Handler: handler}, readiness, time.Minute, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
	}()
	go func() {
		response, _ := http.Get("http://" + listener.Addr().String() + "/readyz")
		if response != nil {
			response.Body.Close()
		}
	}()
	requireReceive(t, started)
	shutdownSignals <- syscall.SIGTERM
	shutdownSignals <- syscall.SIGINT
	requireReceive(t, requestEnded)
	require.NoError(t, <-result)
	assert.Contains(t, logs.String(), "payment-gateway shutdown force requested")
}

type readinessCheckerFunc func(context.Context) error

func (f readinessCheckerFunc) CheckReady(ctx context.Context) error { return f(ctx) }

type runtimeHTTPMetricsFake struct{}

func (runtimeHTTPMetricsFake) RecordHTTPRequest(string, string, int, time.Duration) {}

func discardRuntimeLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testRuntimeHandlerOptions() httpapi.HandlerOptions {
	return validConfig().httpHandler().Options
}
func newTestListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return listener
}

func requireReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for channel")
		var zero T
		return zero
	}
}

func assertChannelNotClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		assert.Fail(t, "channel closed unexpectedly")
	default:
	}
}
