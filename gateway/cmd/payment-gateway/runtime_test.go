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

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/testsupport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeUntilShutdownCancelsStartedRequestAndChangesAvailability(t *testing.T) {
	listener := newTestListener(t)
	started := make(chan struct{})
	requestContextCanceled := make(chan struct{})
	readiness := newShutdownReadiness(readinessCheckerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(requestContextCanceled)
		return ctx.Err()
	}))
	logs := &bytes.Buffer{}
	handler := newRuntimeHandler(t, readiness)
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
	requireReceive(t, requestContextCanceled)
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusNoContent, health.Code)
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, ready.Code)
	completedRequest := requireReceive(t, requestDone)
	require.NoError(t, completedRequest.err)
	defer completedRequest.response.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, completedRequest.response.StatusCode)
	require.NoError(t, <-result)
	assert.Contains(t, logs.String(), "payment-gateway shutdown signal received")
	assert.Contains(t, logs.String(), "payment-gateway shutdown drain started")
	assert.Contains(t, logs.String(), "payment-gateway shutdown completed")
}

func TestServeUntilShutdownCancelsActiveRequestsDuringDrain(t *testing.T) {
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
	handler := newRuntimeHandler(t, readiness)
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
	assert.Contains(t, logs.String(), "payment-gateway shutdown completed")
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
	handler := newRuntimeHandler(t, readiness)
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

func TestServeUntilShutdownRequiresLogger(t *testing.T) {
	err := serveUntilShutdown(nil, nil, nil, 0, nil, nil)

	require.EqualError(t, err, "runtime logger is required")
}

func TestRunIdempotencyReplayCleanupUsesReplayWindowAndStopsWithContext(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	cutoffs := make(chan time.Time, 1)
	store := cleanupPaymentStore{
		PaymentStore: testsupport.NewPaymentStore(),
		cleanup: func(_ context.Context, completedBefore time.Time) (int, error) {
			cutoffs <- completedBefore
			return 3, nil
		},
	}
	logs := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runIdempotencyReplayCleanup(ctx, store, testsupport.FixedClock{Time: now}, slog.New(slog.NewJSONHandler(logs, nil)), time.Millisecond)
	}()

	assert.Equal(t, now.Add(-idempotencyReplayWindow), requireReceive(t, cutoffs))
	cancel()
	requireReceive(t, done)
	assert.Contains(t, logs.String(), "idempotency replay cleanup completed")
}

type readinessCheckerFunc func(context.Context) error

func (f readinessCheckerFunc) CheckReady(ctx context.Context) error { return f(ctx) }

type cleanupPaymentStore struct {
	app.PaymentStore
	cleanup func(context.Context, time.Time) (int, error)
}

func (s cleanupPaymentStore) CleanupCompletedIdempotencyRecords(ctx context.Context, completedBefore time.Time) (int, error) {
	return s.cleanup(ctx, completedBefore)
}

type runtimeHTTPMetricsFake struct{}

func (runtimeHTTPMetricsFake) RecordHTTPRequest(string, string, int, time.Duration) {}

type runtimePaymentApplicationFake struct{}

func (runtimePaymentApplicationFake) AuthorizePayment(context.Context, app.AuthorizePaymentCommand) (app.PaymentCommandResult, error) {
	return app.PaymentCommandResult{}, nil
}

func (runtimePaymentApplicationFake) RetryAuthorization(context.Context, app.RetryAuthorizationCommand) (app.PaymentCommandResult, error) {
	return app.PaymentCommandResult{}, nil
}

func (runtimePaymentApplicationFake) CapturePayment(context.Context, app.CapturePaymentCommand) (app.PaymentCommandResult, error) {
	return app.PaymentCommandResult{}, nil
}

func (runtimePaymentApplicationFake) VoidPayment(context.Context, app.VoidPaymentCommand) (app.PaymentCommandResult, error) {
	return app.PaymentCommandResult{}, nil
}

func (runtimePaymentApplicationFake) RefundPayment(context.Context, app.RefundPaymentCommand) (app.PaymentCommandResult, error) {
	return app.PaymentCommandResult{}, nil
}

func (runtimePaymentApplicationFake) GetPayment(context.Context, app.GetPaymentQuery) (app.PaymentResult, error) {
	return app.PaymentResult{}, nil
}

func (runtimePaymentApplicationFake) SearchPayments(context.Context, app.SearchPaymentsQuery) ([]app.PaymentResult, error) {
	return nil, nil
}

func newRuntimeHandler(t *testing.T, readiness *shutdownReadiness) *httpapi.Handler {
	t.Helper()

	handler, err := httpapi.NewHandler(runtimePaymentApplicationFake{}, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, http.NotFoundHandler(), testRuntimeHandlerOptions())
	require.NoError(t, err)
	return handler
}

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
