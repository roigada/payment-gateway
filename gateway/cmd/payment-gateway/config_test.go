package main

import (
	"net/url"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/serviceauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validDatabaseURL       = "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable"
	validFingerprintSecret = "secret"
	validCredentialKey     = "01234567890123456789012345678901"
)

var validMockBankBaseURL = url.URL{Scheme: "http", Host: "localhost:9090"}

func validConfig() config {
	return config{
		LogLevel: defaultLogLevel, ShutdownTimeout: defaultShutdownTimeout, IdempotencyReplayCleanupInterval: defaultIdempotencyReplayCleanupInterval,
		DatabaseURL: validDatabaseURL, DatabaseMaxOpenConnections: defaultDatabaseMaxOpenConnections, DatabaseMaxIdleConnections: defaultDatabaseMaxIdleConnections, DatabaseConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime, DatabaseConnectionMaxIdleTime: defaultDatabaseConnectionMaxIdleTime, DatabaseStartupTimeout: defaultDatabaseStartupTimeout,
		HTTPAddr: defaultHTTPAddr, HTTPReadHeaderTimeout: defaultHTTPReadHeaderTimeout, HTTPReadTimeout: defaultHTTPReadTimeout, HTTPWriteTimeout: defaultHTTPWriteTimeout, HTTPIdleTimeout: defaultHTTPIdleTimeout, HTTPMaxRequestBodyBytes: defaultHTTPMaxRequestBodyBytes, RateLimitReadRequestsPerSecond: defaultPaymentReadRateLimitRequestsPerSecond, RateLimitReadBurst: defaultPaymentReadRateLimitBurst, RateLimitWriteRequestsPerSecond: defaultPaymentWriteRateLimitRequestsPerSecond, RateLimitWriteBurst: defaultPaymentWriteRateLimitBurst, PaymentCommandTimeout: defaultPaymentCommandTimeout, PaymentReadTimeout: defaultPaymentReadTimeout, ReadinessTimeout: defaultReadinessCheckTimeout,
		MetricsAddr: defaultMetricsAddr, MetricsReadHeaderTimeout: defaultMetricsReadHeaderTimeout, MetricsReadTimeout: defaultMetricsReadTimeout, MetricsWriteTimeout: defaultMetricsWriteTimeout, MetricsIdleTimeout: defaultMetricsIdleTimeout,
		FingerprintSecret: validFingerprintSecret, IdempotencyClaimStuckAfter: defaultIdempotencyClaimStuckAfter,
		ServiceCredentialHMACKey: []byte(validCredentialKey), ServiceCredentials: []serviceauth.Credential{{Digest: serviceauth.Digest([]byte(validCredentialKey), "test-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsRead, serviceauth.ScopePaymentsWrite}}},
		MockBankBaseURL: validMockBankBaseURL, MockBankTimeout: defaultMockBankTimeout, MockBankInitialAttemptTimeout: defaultMockBankInitialAttemptTimeout, MockBankRetryDelay: defaultMockBankRetryDelay, MockBankRetryAttemptTimeout: defaultMockBankRetryAttemptTimeout, MockBankConnectTimeout: defaultMockBankConnectTimeout, MockBankTLSHandshakeTimeout: defaultMockBankTLSHandshakeTimeout, MockBankResponseHeaderTimeout: defaultMockBankResponseHeaderTimeout, MockBankIdleConnectionTimeout: defaultMockBankIdleConnectionTimeout,
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config)
		wantErr string
	}{
		{"valid", func(*config) {}, ""},
		{"public listener address", func(c *config) { c.HTTPAddr = "invalid" }, "ADDR must be a host:port address"},
		{"metrics listener address", func(c *config) { c.MetricsAddr = "" }, "METRICS_ADDR is required"},
		{"metrics listener empty port", func(c *config) { c.MetricsAddr = "localhost:" }, "METRICS_ADDR must be a host:port address"},
		{"metrics listener port range", func(c *config) { c.MetricsAddr = ":65536" }, "METRICS_ADDR must use a port between 1 and 65535"},
		{"shared listener address", func(c *config) { c.MetricsAddr = c.HTTPAddr }, "METRICS_ADDR must differ from ADDR"},
		{"database URL", func(c *config) { c.DatabaseURL = "" }, "DATABASE_URL is required"},
		{"mock bank URL", func(c *config) { c.MockBankBaseURL = url.URL{Path: "relative"} }, "MOCK_BANK_BASE_URL must be an absolute URL"},
		{"pool size", func(c *config) { c.DatabaseMaxOpenConnections = 0 }, "DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer"},
		{"pool relationship", func(c *config) { c.DatabaseMaxIdleConnections = c.DatabaseMaxOpenConnections + 1 }, "DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS"},
		{"payment secret", func(c *config) { c.FingerprintSecret = "" }, "FINGERPRINT_SECRET is required"},
		{"readiness timeout", func(c *config) { c.ReadinessTimeout = 0 }, "READINESS_CHECK_TIMEOUT must be a positive duration"},
		{"read rate", func(c *config) { c.RateLimitReadRequestsPerSecond = 0 }, "RATE_LIMIT_READ_REQUESTS_PER_SECOND must be a positive integer"},
		{"read burst", func(c *config) { c.RateLimitReadBurst = 0 }, "RATE_LIMIT_READ_BURST must be a positive integer"},
		{"write rate", func(c *config) { c.RateLimitWriteRequestsPerSecond = 0 }, "RATE_LIMIT_WRITE_REQUESTS_PER_SECOND must be a positive integer"},
		{"write burst", func(c *config) { c.RateLimitWriteBurst = 0 }, "RATE_LIMIT_WRITE_BURST must be a positive integer"},
		{"mock bank initial attempt", func(c *config) { c.MockBankInitialAttemptTimeout = 0 }, "MOCK_BANK_INITIAL_ATTEMPT_TIMEOUT must be a positive duration"},
		{"mock bank timeout", func(c *config) { c.MockBankTimeout = 0 }, "MOCK_BANK_TIMEOUT must be a positive duration"},
		{"idempotency claim stuck-after budget", func(c *config) { c.IdempotencyClaimStuckAfter = c.PaymentCommandTimeout }, "IDEMPOTENCY_CLAIM_STUCK_AFTER must exceed PAYMENT_COMMAND_TIMEOUT"},
		{"mock bank timeout budget", func(c *config) { c.MockBankTimeout = c.PaymentCommandTimeout }, "MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT"},
		{"mock bank retry delay", func(c *config) { c.MockBankRetryDelay = 0 }, "MOCK_BANK_RETRY_DELAY must be a positive duration"},
		{"mock bank retry attempt", func(c *config) { c.MockBankRetryAttemptTimeout = 0 }, "MOCK_BANK_RETRY_ATTEMPT_TIMEOUT must be a positive duration"},
		{"mock bank retry budget", func(c *config) { c.MockBankRetryAttemptTimeout = c.PaymentCommandTimeout }, "Mock Bank retry budget must leave time within PAYMENT_COMMAND_TIMEOUT"},
		{"HTTP write budget", func(c *config) { c.HTTPWriteTimeout = c.PaymentCommandTimeout }, "HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT"},
		{"metrics read timeout", func(c *config) { c.MetricsReadTimeout = 0 }, "METRICS_READ_TIMEOUT must be a positive duration"},
		{"log level", func(c *config) { c.LogLevel = "verbose" }, "LOG_LEVEL must be one of debug, info, warn, or error"},
		{"idempotency replay cleanup interval", func(c *config) { c.IdempotencyReplayCleanupInterval = 0 }, "IDEMPOTENCY_REPLAY_CLEANUP_INTERVAL must be a positive duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			err := cfg.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestConfigHandlerConfig(t *testing.T) {
	cfg := validConfig()

	assert.Equal(t, httpapi.HandlerConfig{
		PaymentCommandTimeout: cfg.PaymentCommandTimeout,
		PaymentReadTimeout:    cfg.PaymentReadTimeout,
		ReadinessTimeout:      cfg.ReadinessTimeout,
		MaxRequestBodyBytes:   cfg.HTTPMaxRequestBodyBytes,
		RateLimit:             cfg.rateLimitConfig(),
	}, cfg.handlerConfig())
}

func TestLoadConfigUsesDefaults(t *testing.T) {
	setRequiredConfigEnv(t)
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, defaultHTTPAddr, cfg.HTTPAddr)
	assert.Equal(t, defaultMetricsAddr, cfg.MetricsAddr)
	assert.Equal(t, defaultMetricsReadHeaderTimeout, cfg.MetricsReadHeaderTimeout)
	assert.Equal(t, defaultMetricsWriteTimeout, cfg.MetricsWriteTimeout)
	assert.Equal(t, defaultMetricsIdleTimeout, cfg.MetricsIdleTimeout)
	assert.Equal(t, validDatabaseURL, cfg.DatabaseURL)
	assert.Equal(t, defaultDatabaseMaxOpenConnections, cfg.DatabaseMaxOpenConnections)
	assert.Equal(t, defaultDatabaseConnectionMaxIdleTime, cfg.DatabaseConnectionMaxIdleTime)
	assert.Equal(t, defaultPaymentCommandTimeout, cfg.PaymentCommandTimeout)
	assert.Equal(t, defaultReadinessCheckTimeout, cfg.ReadinessTimeout)
	assert.Equal(t, httpapi.RateLimitConfig{ReadRequestsPerSecond: defaultPaymentReadRateLimitRequestsPerSecond, ReadBurst: defaultPaymentReadRateLimitBurst, WriteRequestsPerSecond: defaultPaymentWriteRateLimitRequestsPerSecond, WriteBurst: defaultPaymentWriteRateLimitBurst}, cfg.rateLimitConfig())
	assert.Equal(t, defaultMockBankInitialAttemptTimeout, cfg.MockBankInitialAttemptTimeout)
	assert.Equal(t, defaultMockBankRetryDelay, cfg.MockBankRetryDelay)
	assert.Equal(t, defaultMockBankRetryAttemptTimeout, cfg.MockBankRetryAttemptTimeout)
	assert.Equal(t, validMockBankBaseURL, cfg.MockBankBaseURL)
	assert.Equal(t, defaultLogLevel, cfg.LogLevel)
	assert.Equal(t, defaultShutdownTimeout, cfg.ShutdownTimeout)
	assert.Equal(t, defaultIdempotencyReplayCleanupInterval, cfg.IdempotencyReplayCleanupInterval)
}

func TestLoadConfigAllowsComponentConfiguration(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("DATABASE_MAX_OPEN_CONNECTIONS", "20")
	t.Setenv("DATABASE_MAX_IDLE_CONNECTIONS", "8")
	t.Setenv("DATABASE_CONNECTION_MAX_LIFETIME", "45m")
	t.Setenv("PAYMENT_COMMAND_TIMEOUT", "12s")
	t.Setenv("READINESS_CHECK_TIMEOUT", "4s")
	t.Setenv("MOCK_BANK_INITIAL_ATTEMPT_TIMEOUT", "3s")
	t.Setenv("MOCK_BANK_RETRY_DELAY", "500ms")
	t.Setenv("MOCK_BANK_RETRY_ATTEMPT_TIMEOUT", "6s")
	t.Setenv("HTTP_MAX_REQUEST_BODY_BYTES", "32768")
	t.Setenv("RATE_LIMIT_READ_REQUESTS_PER_SECOND", "31")
	t.Setenv("RATE_LIMIT_READ_BURST", "61")
	t.Setenv("RATE_LIMIT_WRITE_REQUESTS_PER_SECOND", "6")
	t.Setenv("RATE_LIMIT_WRITE_BURST", "11")
	t.Setenv("METRICS_ADDR", "127.0.0.1:9191")
	t.Setenv("METRICS_READ_TIMEOUT", "4s")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("IDEMPOTENCY_REPLAY_CLEANUP_INTERVAL", "30m")
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.DatabaseMaxOpenConnections)
	assert.Equal(t, 8, cfg.DatabaseMaxIdleConnections)
	assert.Equal(t, 45*time.Minute, cfg.DatabaseConnectionMaxLifetime)
	assert.Equal(t, 12*time.Second, cfg.PaymentCommandTimeout)
	assert.Equal(t, 4*time.Second, cfg.ReadinessTimeout)
	assert.Equal(t, 3*time.Second, cfg.MockBankInitialAttemptTimeout)
	assert.Equal(t, 500*time.Millisecond, cfg.MockBankRetryDelay)
	assert.Equal(t, 6*time.Second, cfg.MockBankRetryAttemptTimeout)
	assert.Equal(t, int64(32768), cfg.HTTPMaxRequestBodyBytes)
	assert.Equal(t, httpapi.RateLimitConfig{ReadRequestsPerSecond: 31, ReadBurst: 61, WriteRequestsPerSecond: 6, WriteBurst: 11}, cfg.rateLimitConfig())
	assert.Equal(t, "127.0.0.1:9191", cfg.MetricsAddr)
	assert.Equal(t, 4*time.Second, cfg.MetricsReadTimeout)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 30*time.Minute, cfg.IdempotencyReplayCleanupInterval)
}

func TestLoadConfigRejectsMalformedValues(t *testing.T) {
	for _, tt := range []struct{ name, value string }{
		{"DATABASE_MAX_OPEN_CONNECTIONS", "many"},
		{"DATABASE_CONNECTION_MAX_IDLE_TIME", "forever"},
		{"PAYMENT_COMMAND_TIMEOUT", "forever"},
		{"READINESS_CHECK_TIMEOUT", "forever"},
		{"MOCK_BANK_RETRY_DELAY", "later"},
		{"MOCK_BANK_BASE_URL", "https://%"},
		{"HTTP_MAX_REQUEST_BODY_BYTES", "large"},
		{"RATE_LIMIT_READ_REQUESTS_PER_SECOND", "many"},
		{"RATE_LIMIT_READ_BURST", "many"},
		{"RATE_LIMIT_WRITE_REQUESTS_PER_SECOND", "many"},
		{"RATE_LIMIT_WRITE_BURST", "many"},
		{"MOCK_BANK_CONNECT_TIMEOUT", "forever"},
		{"METRICS_READ_TIMEOUT", "forever"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredConfigEnv(t)
			t.Setenv(tt.name, tt.value)
			cfg, err := loadConfig()
			require.Error(t, err)
			assert.Equal(t, config{}, cfg)
		})
	}
}

func setRequiredConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", validDatabaseURL)
	t.Setenv("MOCK_BANK_BASE_URL", validMockBankBaseURL.String())
	t.Setenv("FINGERPRINT_SECRET", validFingerprintSecret)
	t.Setenv("SERVICE_CREDENTIAL_HMAC_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE")
	t.Setenv("ORDER_SERVICE_CREDENTIALS", serviceauth.Digest([]byte(validCredentialKey), "test-credential")+"=payments:read+payments:write")
}

func TestParseServiceCredentials(t *testing.T) {
	credentials, err := parseServiceCredentials("first=payments:read,second=payments:read+payments:write")
	require.NoError(t, err)
	assert.Equal(t, []serviceauth.Credential{{Digest: "first", Scopes: []serviceauth.Scope{"payments:read"}}, {Digest: "second", Scopes: []serviceauth.Scope{"payments:read", "payments:write"}}}, credentials)

	_, err = parseServiceCredentials("missing-separator")
	require.Error(t, err)
}
