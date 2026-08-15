package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/observability"
	"github.com/roigada/payment-gateway/internal/serviceauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrivateMetricsHandlerIsExcludedFromPaymentRateLimits(t *testing.T) {
	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	options := testRuntimeHandlerOptions(t)
	options.RateLimit = httpapi.RateLimitConfig{ReadRequestsPerSecond: 1, ReadBurst: 1, WriteRequestsPerSecond: 1, WriteBurst: 1}
	publicHandler := newRuntimeHandlerWithOptions(t, readiness, options)

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

func TestPublicAndMetricsServersExposeSeparateEndpoints(t *testing.T) {
	readiness := newShutdownReadiness(readinessCheckerFunc(func(context.Context) error { return nil }))
	_, publicListener := newTestServer(t, newRuntimeHandler(t, readiness))
	_, metricsListener := newTestServer(t, observability.NewHandler(observability.NewRegistry()))

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
}

func TestNewHTTPServerConfiguresAddress(t *testing.T) {
	server := newHTTPServer(http.NotFoundHandler(), ServerConfig{Addr: "127.0.0.1:8080"})

	assert.Equal(t, "127.0.0.1:8080", server.Addr)
}

func TestListenAndServeReportsListenFailure(t *testing.T) {
	results := make(chan error, 1)
	go listenAndServe(&http.Server{Addr: "127.0.0.1:-1"}, results)

	select {
	case err := <-results:
		require.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for listen failure")
	}
}

func TestListenAndServeNormalizesServerClosed(t *testing.T) {
	server := &http.Server{Addr: "127.0.0.1:0"}
	_ = server.Shutdown(context.Background())
	results := make(chan error, 1)
	go listenAndServe(server, results)

	require.NoError(t, requireReceive(t, results))
}

func TestShutdownServersLetsActiveRequestsFinishAndStopsBothServers(t *testing.T) {
	requestStarted := make(chan struct{})
	finishRequest := make(chan struct{})
	publicServer, publicListener := newTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(requestStarted)
		<-finishRequest
	}))
	metricsServer, metricsListener := newTestServer(t, http.NotFoundHandler())
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + publicListener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	requireReceive(t, requestStarted)

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownServers(context.Background(), publicServer, metricsServer)
	}()
	assert.Never(t, func() bool {
		select {
		case <-shutdownDone:
			return true
		default:
			return false
		}
	}, 50*time.Millisecond, time.Millisecond)

	close(finishRequest)
	require.NoError(t, requireReceive(t, requestDone))
	require.NoError(t, requireReceive(t, shutdownDone))
	requireServerUnavailable(t, publicListener.Addr().String())
	requireServerUnavailable(t, metricsListener.Addr().String())
}

func TestCloseServersCancelsActiveRequestsAndStopsBothServers(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	publicServer, publicListener := newTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
		close(requestCanceled)
	}))
	metricsServer, metricsListener := newTestServer(t, http.NotFoundHandler())
	go func() {
		response, _ := http.Get("http://" + publicListener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
	}()
	requireReceive(t, requestStarted)

	require.NoError(t, closeServers(publicServer, metricsServer))
	requireReceive(t, requestCanceled)
	requireServerUnavailable(t, publicListener.Addr().String())
	requireServerUnavailable(t, metricsListener.Addr().String())
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

	return newRuntimeHandlerWithOptions(t, readiness, testRuntimeHandlerOptions(t))
}

func newRuntimeHandlerWithOptions(t *testing.T, readiness *shutdownReadiness, options httpapi.HandlerOptions) *httpapi.Handler {
	t.Helper()
	cfg := validConfig()
	authenticator, err := serviceauth.NewAuthenticator(cfg.Auth.HMACKey, cfg.Auth.Credentials)
	require.NoError(t, err)
	handler, err := httpapi.NewHandler(runtimePaymentApplicationFake{}, readiness, discardRuntimeLogger(), runtimeHTTPAPIMetricsFake{}, authenticator, app.SystemClock{}, options)
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
		ReadinessTimeout:      cfg.HTTP.ReadinessTimeout,
		MaxRequestBodyBytes:   cfg.HTTP.MaxRequestBodyBytes,
		RateLimit:             cfg.HTTP.RateLimit,
	}
}

func newTestServer(t *testing.T, handler http.Handler) (*http.Server, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return server, listener
}

func requireReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for result")
		var zero T
		return zero
	}
}

func requireServerUnavailable(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Get("http://" + addr)
	if response != nil {
		response.Body.Close()
	}
	require.Error(t, err)
}
