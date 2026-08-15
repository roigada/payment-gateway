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
	"github.com/roigada/payment-gateway/internal/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeUntilShutdownChangesAvailability(t *testing.T) {
	listener := newTestListener(t)
	started := make(chan struct{})
	readiness := newShutdownReadiness(readinessCheckerFunc(func(ctx context.Context) error {
		close(started)
		return nil
	}))
	logs := &bytes.Buffer{}
	handler := newRuntimeHandler(t, readiness)
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdownAll([]runtimeServer{{listener: listener, server: &http.Server{Handler: handler}}}, readiness, time.Second, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
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
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusNoContent, health.Code)
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, ready.Code)
	completedRequest := requireReceive(t, requestDone)
	require.NoError(t, completedRequest.err)
	defer completedRequest.response.Body.Close()
	assert.Equal(t, http.StatusNoContent, completedRequest.response.StatusCode)
	require.NoError(t, <-result)
	assert.Contains(t, logs.String(), "payment-gateway shutdown signal received")
	assert.Contains(t, logs.String(), "payment-gateway shutdown drain started")
	assert.Contains(t, logs.String(), "payment-gateway shutdown completed")
}

func TestServeUntilShutdownLetsActiveRequestsFinishDuringDrain(t *testing.T) {
	listener := newTestListener(t)
	started := make(chan struct{})
	requestContextCanceled := make(chan struct{})
	finishRequest := make(chan struct{})
	readiness := newShutdownReadiness(readinessCheckerFunc(func(ctx context.Context) error {
		close(started)
		select {
		case <-ctx.Done():
			close(requestContextCanceled)
			return ctx.Err()
		case <-finishRequest:
			return nil
		}
	}))
	logs := &bytes.Buffer{}
	handler := newRuntimeHandler(t, readiness)
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdownAll([]runtimeServer{{listener: listener, server: &http.Server{Handler: handler}}}, readiness, time.Second, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
	}()
	go func() {
		response, _ := http.Get("http://" + listener.Addr().String() + "/readyz")
		if response != nil {
			response.Body.Close()
		}
	}()
	requireReceive(t, started)
	shutdownSignals <- syscall.SIGINT
	require.Eventually(t, func() bool {
		return readiness.draining.Load()
	}, time.Second, time.Millisecond)
	assert.Never(t, func() bool {
		select {
		case <-requestContextCanceled:
			return true
		default:
			return false
		}
	}, 50*time.Millisecond, time.Millisecond)
	close(finishRequest)
	require.NoError(t, <-result)
	assert.Contains(t, logs.String(), "payment-gateway shutdown completed")
}

func TestServeUntilShutdownSeparatesMetricsAndStopsBothListeners(t *testing.T) {
	publicListener := newTestListener(t)
	metricsListener := newTestListener(t)
	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	publicHandler := newRuntimeHandler(t, readiness)
	metricsHandler := observability.NewHandler(observability.NewRegistry())
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilShutdownAll([]runtimeServer{
			{listener: publicListener, server: &http.Server{Handler: publicHandler}},
			{listener: metricsListener, server: &http.Server{Handler: metricsHandler}},
		}, readiness, time.Second, shutdownSignals, discardRuntimeLogger())
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
	assert.NotEmpty(t, body)
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

func TestPrivateMetricsHandlerIsExcludedFromPaymentRateLimits(t *testing.T) {
	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	options := testRuntimeHandlerOptions(t)
	dependencies := testRuntimeHandlerDependencies(t, runtimePaymentApplicationFake{}, readiness, discardRuntimeLogger(), runtimeHTTPAPIMetricsFake{})
	options.RateLimit = httpapi.RateLimitConfig{ReadRequestsPerSecond: 1, ReadBurst: 1, WriteRequestsPerSecond: 1, WriteBurst: 1}
	publicHandler, err := httpapi.NewHandler(dependencies, options)
	require.NoError(t, err)

	for requestNumber := range 2 {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/payments?order_id=order-1", nil)
		request.Header.Set("Authorization", "Bearer test-credential")
		response := httptest.NewRecorder()
		publicHandler.ServeHTTP(response, request)
		if requestNumber == 0 {
			require.NotEqual(t, http.StatusTooManyRequests, response.Code)
			continue
		}
		require.Equal(t, http.StatusTooManyRequests, response.Code)
	}

	metricsHandler := observability.NewHandler(observability.NewRegistry())
	metrics := httptest.NewRecorder()
	metricsHandler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusOK, metrics.Code)
	assert.NotEmpty(t, metrics.Body.String())
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
		}, readiness, time.Second, make(chan os.Signal), discardRuntimeLogger())
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
		result <- serveUntilShutdownAll([]runtimeServer{{listener: listener, server: &http.Server{Handler: handler}}}, readiness, time.Minute, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
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
	err := serveUntilShutdownAll(nil, nil, 0, nil, nil)

	require.EqualError(t, err, "runtime logger is required")
}

type readinessCheckerFunc func(context.Context) error

func (f readinessCheckerFunc) CheckReady(ctx context.Context) error { return f(ctx) }

type runtimeHTTPAPIMetricsFake struct{}

func (runtimeHTTPAPIMetricsFake) RecordHTTPRequest(string, string, int, time.Duration) {}
func (runtimeHTTPAPIMetricsFake) RecordRateLimitRejection(string)                      {}

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

	handler, err := httpapi.NewHandler(testRuntimeHandlerDependencies(t, runtimePaymentApplicationFake{}, readiness, discardRuntimeLogger(), runtimeHTTPAPIMetricsFake{}), testRuntimeHandlerOptions(t))
	require.NoError(t, err)
	return handler
}

func discardRuntimeLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testRuntimeHandlerOptions(t *testing.T) httpapi.HandlerOptions {
	t.Helper()
	cfg := validConfig()
	return httpapi.HandlerOptions{
		PaymentCommandTimeout: cfg.HTTP.PaymentCommandTimeout,
		PaymentReadTimeout:    cfg.HTTP.PaymentReadTimeout,
		ReadinessTimeout:      readinessCheckTimeout,
		MaxRequestBodyBytes:   cfg.HTTP.MaxRequestBodyBytes,
		RateLimit:             cfg.HTTP.RateLimit,
	}
}

func testRuntimeHandlerDependencies(t *testing.T, payments runtimePaymentApplicationFake, readiness *shutdownReadiness, logger *slog.Logger, metrics runtimeHTTPAPIMetricsFake) httpapi.HandlerDependencies {
	t.Helper()
	cfg := validConfig()
	authenticator, err := cfg.Auth.authenticator()
	require.NoError(t, err)
	return httpapi.HandlerDependencies{
		Payments:      payments,
		Readiness:     readiness,
		Logger:        logger,
		Metrics:       metrics,
		Authenticator: authenticator,
		Clock:         app.SystemClock{},
	}
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
