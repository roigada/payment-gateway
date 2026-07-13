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

const (
	validDatabaseURL       = "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable"
	validMockBankBaseURL   = "http://localhost:9090"
	validFingerprintSecret = "secret"
)

func validConfig() config {
	return config{
		DatabaseURL:                   validDatabaseURL,
		DatabaseMaxOpenConnections:    defaultDatabaseMaxOpenConnections,
		DatabaseMaxIdleConnections:    defaultDatabaseMaxIdleConnections,
		DatabaseConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime,
		MockBankBaseURL:               validMockBankBaseURL,
		FingerprintSecret:             validFingerprintSecret,
		IdempotencyClaimStuckAfter:    defaultIdempotencyClaimStuckAfter,
		ShutdownTimeout:               defaultShutdownTimeout,
	}
}

func TestConfigValidateRequiresPaymentGatewayConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config
		wantErr string
	}{
		{
			name:    "database URL",
			cfg:     config{},
			wantErr: "DATABASE_URL is required",
		},
		{
			name: "mock bank base URL",
			cfg: config{
				DatabaseURL:                   validDatabaseURL,
				DatabaseMaxOpenConnections:    defaultDatabaseMaxOpenConnections,
				DatabaseMaxIdleConnections:    defaultDatabaseMaxIdleConnections,
				DatabaseConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime,
				IdempotencyClaimStuckAfter:    defaultIdempotencyClaimStuckAfter,
			},
			wantErr: "MOCK_BANK_BASE_URL is required",
		},
		{
			name: "fingerprint secret",
			cfg: config{
				DatabaseURL:                   validDatabaseURL,
				DatabaseMaxOpenConnections:    defaultDatabaseMaxOpenConnections,
				DatabaseMaxIdleConnections:    defaultDatabaseMaxIdleConnections,
				DatabaseConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime,
				MockBankBaseURL:               validMockBankBaseURL,
				IdempotencyClaimStuckAfter:    defaultIdempotencyClaimStuckAfter,
			},
			wantErr: "FINGERPRINT_SECRET is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfigValidateAllowsPaymentGatewayConfiguration(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.validate())
}

func TestConfigValidateRequiresDatabasePoolConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config)
		wantErr string
	}{
		{
			name: "max open connections",
			mutate: func(cfg *config) {
				cfg.DatabaseMaxOpenConnections = 0
			},
			wantErr: "DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer",
		},
		{
			name: "max idle connections",
			mutate: func(cfg *config) {
				cfg.DatabaseMaxIdleConnections = -1
			},
			wantErr: "DATABASE_MAX_IDLE_CONNECTIONS must be a non-negative integer",
		},
		{
			name: "max idle greater than max open",
			mutate: func(cfg *config) {
				cfg.DatabaseMaxOpenConnections = 5
				cfg.DatabaseMaxIdleConnections = 6
			},
			wantErr: "DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS",
		},
		{
			name: "connection max lifetime",
			mutate: func(cfg *config) {
				cfg.DatabaseConnectionMaxLifetime = 0
			},
			wantErr: "DATABASE_CONNECTION_MAX_LIFETIME must be a positive duration",
		},
		{
			name: "idempotency claim stuck-after",
			mutate: func(cfg *config) {
				cfg.IdempotencyClaimStuckAfter = 0
			},
			wantErr: "IDEMPOTENCY_CLAIM_STUCK_AFTER must be a positive duration",
		},
		{
			name: "shutdown timeout",
			mutate: func(cfg *config) {
				cfg.ShutdownTimeout = 0
			},
			wantErr: "SHUTDOWN_TIMEOUT must be a positive duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadConfigUsesPaymentGatewayRuntimeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", validDatabaseURL)
	t.Setenv("MOCK_BANK_BASE_URL", validMockBankBaseURL)
	t.Setenv("FINGERPRINT_SECRET", validFingerprintSecret)

	cfg, err := loadConfig()

	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, validDatabaseURL, cfg.DatabaseURL)
	assert.Equal(t, defaultDatabaseMaxOpenConnections, cfg.DatabaseMaxOpenConnections)
	assert.Equal(t, defaultDatabaseMaxIdleConnections, cfg.DatabaseMaxIdleConnections)
	assert.Equal(t, defaultDatabaseConnectionMaxLifetime, cfg.DatabaseConnectionMaxLifetime)
	assert.Equal(t, validMockBankBaseURL, cfg.MockBankBaseURL)
	assert.Equal(t, validFingerprintSecret, cfg.FingerprintSecret)
	assert.Equal(t, defaultIdempotencyClaimStuckAfter, cfg.IdempotencyClaimStuckAfter)
	assert.Equal(t, defaultShutdownTimeout, cfg.ShutdownTimeout)
}

func TestLoadConfigAllowsCustomDurationConfiguration(t *testing.T) {
	t.Setenv("DATABASE_MAX_OPEN_CONNECTIONS", "20")
	t.Setenv("DATABASE_MAX_IDLE_CONNECTIONS", "8")
	t.Setenv("DATABASE_CONNECTION_MAX_LIFETIME", "45m")
	t.Setenv("IDEMPOTENCY_CLAIM_STUCK_AFTER", "2m")
	t.Setenv("SHUTDOWN_TIMEOUT", "45s")

	cfg, err := loadConfig()

	require.NoError(t, err)
	assert.Equal(t, 20, cfg.DatabaseMaxOpenConnections)
	assert.Equal(t, 8, cfg.DatabaseMaxIdleConnections)
	assert.Equal(t, 45*time.Minute, cfg.DatabaseConnectionMaxLifetime)
	assert.Equal(t, 2*time.Minute, cfg.IdempotencyClaimStuckAfter)
	assert.Equal(t, 45*time.Second, cfg.ShutdownTimeout)
}

func TestLoadConfigRejectsMalformedDatabasePoolConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		envName string
		value   string
		wantErr string
	}{
		{
			name:    "max open connections",
			envName: "DATABASE_MAX_OPEN_CONNECTIONS",
			value:   "many",
			wantErr: "DATABASE_MAX_OPEN_CONNECTIONS must be an integer",
		},
		{
			name:    "max idle connections",
			envName: "DATABASE_MAX_IDLE_CONNECTIONS",
			value:   "some",
			wantErr: "DATABASE_MAX_IDLE_CONNECTIONS must be an integer",
		},
		{
			name:    "connection max lifetime",
			envName: "DATABASE_CONNECTION_MAX_LIFETIME",
			value:   "forever",
			wantErr: "DATABASE_CONNECTION_MAX_LIFETIME must be a valid duration",
		},
		{
			name:    "idempotency claim stuck-after",
			envName: "IDEMPOTENCY_CLAIM_STUCK_AFTER",
			value:   "forever",
			wantErr: "IDEMPOTENCY_CLAIM_STUCK_AFTER must be a valid duration",
		},
		{
			name:    "shutdown timeout",
			envName: "SHUTDOWN_TIMEOUT",
			value:   "forever",
			wantErr: "SHUTDOWN_TIMEOUT must be a valid duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envName, tt.value)

			cfg, err := loadConfig()

			require.Error(t, err)
			assert.Equal(t, config{}, cfg)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestRunHTTPServerDrainsStartedRequestAndChangesAvailability(t *testing.T) {
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
	handler := httpapi.NewServer(nil, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, nil)
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- runHTTPServer(listener, handler, readiness, time.Second, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
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

func TestRunHTTPServerForceClosesRequestsAfterDrainDeadline(t *testing.T) {
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
	handler := httpapi.NewServer(nil, readiness, discardRuntimeLogger(), runtimeHTTPMetricsFake{}, nil)
	shutdownSignals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- runHTTPServer(listener, handler, readiness, 10*time.Millisecond, shutdownSignals, slog.New(slog.NewJSONHandler(logs, nil)))
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

type readinessCheckerFunc func(context.Context) error

func (f readinessCheckerFunc) CheckReady(ctx context.Context) error { return f(ctx) }

type runtimeHTTPMetricsFake struct{}

func (runtimeHTTPMetricsFake) RecordHTTPRequest(string, string, int, time.Duration) {}

func discardRuntimeLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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

func TestLoadConfigAllowsCustomListenAddress(t *testing.T) {
	t.Setenv("ADDR", ":9090")

	cfg, err := loadConfig()

	require.NoError(t, err)
	assert.Equal(t, ":9090", cfg.Addr)
}

func TestConfigureDatabasePoolAppliesConfig(t *testing.T) {
	pool := &recordingDatabasePool{}
	cfg := validConfig()
	cfg.DatabaseMaxOpenConnections = 20
	cfg.DatabaseMaxIdleConnections = 8
	cfg.DatabaseConnectionMaxLifetime = 45 * time.Minute

	configureDatabasePool(pool, cfg)

	assert.Equal(t, 20, pool.maxOpenConnections)
	assert.Equal(t, 8, pool.maxIdleConnections)
	assert.Equal(t, 45*time.Minute, pool.connectionMaxLifetime)
}

type recordingDatabasePool struct {
	maxOpenConnections    int
	maxIdleConnections    int
	connectionMaxLifetime time.Duration
}

func (p *recordingDatabasePool) SetMaxOpenConns(n int) {
	p.maxOpenConnections = n
}

func (p *recordingDatabasePool) SetMaxIdleConns(n int) {
	p.maxIdleConnections = n
}

func (p *recordingDatabasePool) SetConnMaxLifetime(d time.Duration) {
	p.connectionMaxLifetime = d
}
