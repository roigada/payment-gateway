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
		result <- serveUntilShutdown(listener, &http.Server{Handler: handler}, readiness, time.Second, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)), nil)
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
		result <- serveUntilShutdown(listener, &http.Server{Handler: handler}, readiness, 10*time.Millisecond, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)), nil)
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

func TestServeUntilShutdownSeparatesMetricsAndStopsBothListeners(t *testing.T) {
	publicListener := newTestListener(t)
	metricsListener := newTestListener(t)
	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	publicHandler := newRuntimeHandler(t, readiness)
	metricsHandler := newMetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# gateway metrics\n"))
	}))
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdownAll([]runtimeServer{
			{listener: publicListener, server: &http.Server{Handler: publicHandler}},
			{listener: metricsListener, server: &http.Server{Handler: metricsHandler}},
		}, readiness, time.Second, shutdownSignals, discardRuntimeLogger(), nil)
	}()

	publicMetrics, err := http.Get("http://" + publicListener.Addr().String() + "/metrics")
	require.NoError(t, err)
	defer publicMetrics.Body.Close()
	assert.Equal(t, http.StatusNotFound, publicMetrics.StatusCode)

	metrics, err := http.Get("http://" + metricsListener.Addr().String() + "/metrics")
	require.NoError(t, err)
	defer metrics.Body.Close()
	body, err := io.ReadAll(metrics.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, metrics.StatusCode)
	assert.Equal(t, "# gateway metrics\n", string(body))
	metricsFallback, err := http.Get("http://" + metricsListener.Addr().String() + "/not-metrics")
	require.NoError(t, err)
	defer metricsFallback.Body.Close()
	assert.Equal(t, http.StatusNotFound, metricsFallback.StatusCode)

	shutdownSignals <- syscall.SIGTERM
	require.NoError(t, <-result)
	_, err = http.Get("http://" + publicListener.Addr().String() + "/healthz")
	assert.Error(t, err)
	_, err = http.Get("http://" + metricsListener.Addr().String() + "/metrics")
	assert.Error(t, err)
}

func TestServeUntilShutdownStopsPeerWhenListenerFails(t *testing.T) {
	failedListener := newTestListener(t)
	peerListener := newTestListener(t)
	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdownAll([]runtimeServer{
			{listener: failedListener, server: &http.Server{Handler: http.NotFoundHandler()}},
			{listener: peerListener, server: &http.Server{Handler: http.NotFoundHandler()}},
		}, readiness, time.Second, make(chan os.Signal), discardRuntimeLogger(), nil)
	}()

	peerResponse, err := http.Get("http://" + peerListener.Addr().String())
	require.NoError(t, err)
	peerResponse.Body.Close()
	require.NoError(t, failedListener.Close())
	require.Error(t, requireReceive(t, result))
	_, err = http.Get("http://" + peerListener.Addr().String())
	assert.Error(t, err)
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
		result <- serveUntilShutdown(listener, &http.Server{Handler: handler}, readiness, time.Minute, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)), nil)
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
	err := serveUntilShutdown(nil, nil, nil, 0, nil, nil, nil)

	require.EqualError(t, err, "runtime logger is required")
}

func TestRunIdempotencyReplayCleanupUsesReplayWindowRecordsOutcomesAndStopsWithContext(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	cutoffs := make(chan time.Time, 3)
	cleanupCalls := 0
	store := cleanupPaymentStore{
		PaymentStore: testsupport.NewPaymentStore(),
		cleanup: func(_ context.Context, completedBefore time.Time) (int, error) {
			cutoffs <- completedBefore
			cleanupCalls++
			switch cleanupCalls {
			case 1:
				return 0, assert.AnError
			case 2:
				return 0, nil
			default:
				return 3, nil
			}
		},
	}
	logs := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	ticker := &cleanupTickerFake{ticks: make(chan time.Time, 3)}
	metrics := &cleanupMetricsFake{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runIdempotencyReplayCleanup(ctx, store, testsupport.FixedClock{Time: now}, slog.New(slog.NewJSONHandler(logs, nil)), metrics, defaultIdempotencyReplayWindow, ticker)
	}()

	ticker.ticks <- now
	ticker.ticks <- now
	ticker.ticks <- now
	assert.Equal(t, now.Add(-defaultIdempotencyReplayWindow), requireReceive(t, cutoffs))
	assert.Equal(t, now.Add(-defaultIdempotencyReplayWindow), requireReceive(t, cutoffs))
	assert.Equal(t, now.Add(-defaultIdempotencyReplayWindow), requireReceive(t, cutoffs))
	cancel()
	requireReceive(t, done)
	assert.True(t, ticker.stopped)
	assert.Equal(t, []cleanupMetricCall{{result: idempotencyReplayCleanupFailed}, {result: idempotencyReplayCleanupEmpty}, {result: idempotencyReplayCleanupCompleted, removed: 3}}, metrics.calls)
	assert.Contains(t, logs.String(), "idempotency replay cleanup failed")
	assert.Contains(t, logs.String(), "idempotency replay cleanup completed")
	assert.NotContains(t, logs.String(), assert.AnError.Error())
}

func TestServeUntilShutdownCancelsIdempotencyReplayCleanupAtDrainStart(t *testing.T) {
	listener := newTestListener(t)
	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()
	cleanupStarted := make(chan struct{})
	cleanupCanceled := make(chan struct{})
	ticker := &cleanupTickerFake{ticks: make(chan time.Time, 1)}
	store := cleanupPaymentStore{PaymentStore: testsupport.NewPaymentStore(), cleanup: func(ctx context.Context, _ time.Time) (int, error) {
		close(cleanupStarted)
		<-ctx.Done()
		close(cleanupCanceled)
		return 0, ctx.Err()
	}}
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		runIdempotencyReplayCleanup(cleanupCtx, store, testsupport.FixedClock{}, discardRuntimeLogger(), &cleanupMetricsFake{}, defaultIdempotencyReplayWindow, ticker)
	}()
	ticker.ticks <- time.Now()
	requireReceive(t, cleanupStarted)

	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdown(listener, &http.Server{Handler: newRuntimeHandler(t, readiness)}, readiness, time.Second, shutdownSignals, discardRuntimeLogger(), cancelCleanup)
	}()
	shutdownSignals <- syscall.SIGTERM
	requireReceive(t, cleanupCanceled)
	requireReceive(t, cleanupDone)
	require.NoError(t, <-result)
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

type cleanupTickerFake struct {
	ticks   chan time.Time
	stopped bool
}

func (t *cleanupTickerFake) Chan() <-chan time.Time { return t.ticks }
func (t *cleanupTickerFake) Stop()                  { t.stopped = true }

type cleanupMetricCall struct {
	result  string
	removed int
}

type cleanupMetricsFake struct{ calls []cleanupMetricCall }

func (m *cleanupMetricsFake) RecordIdempotencyReplayCleanup(result string, removed int) {
	m.calls = append(m.calls, cleanupMetricCall{result: result, removed: removed})
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

	handler, err := httpapi.NewHandler(runtimePaymentApplicationFake{}, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, testRuntimeHandlerOptions(t))
	require.NoError(t, err)
	return handler
}

func discardRuntimeLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testRuntimeHandlerOptions(t *testing.T) httpapi.HandlerOptions {
	t.Helper()
	cfg := validConfig()
	options := cfg.httpHandler().Options
	authenticator, err := cfg.Auth.authenticator()
	require.NoError(t, err)
	options.Authenticator = authenticator
	limiter, err := httpapi.NewRateLimiter(app.SystemClock{}, cfg.HTTP.RateLimit)
	require.NoError(t, err)
	options.RateLimiter = limiter
	return options
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
