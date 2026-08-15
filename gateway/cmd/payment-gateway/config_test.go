package main

import (
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/serviceauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validDatabaseURL       = "postgres://payment_gateway:payment_gateway@localhost:5432/payment_gateway?sslmode=disable"
	validMockBankBaseURL   = "http://localhost:9090"
	validFingerprintSecret = "secret"
	validCredentialKey     = "01234567890123456789012345678901"
)

func validConfig() config {
	return config{
		Runtime:  RuntimeConfig{LogLevel: defaultLogLevel, ShutdownTimeout: defaultShutdownTimeout, IdempotencyReplayCleanupInterval: defaultIdempotencyReplayCleanupInterval},
		Database: DatabaseConfig{URL: validDatabaseURL, MaxOpenConnections: defaultDatabaseMaxOpenConnections, MaxIdleConnections: defaultDatabaseMaxIdleConnections, ConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime, ConnectionMaxIdleTime: defaultDatabaseConnectionMaxIdleTime, StartupTimeout: defaultDatabaseStartupTimeout},
		HTTP:     HTTPConfig{ServerConfig: ServerConfig{Addr: defaultHTTPAddr, ReadHeaderTimeout: defaultHTTPReadHeaderTimeout, ReadTimeout: defaultHTTPReadTimeout, WriteTimeout: defaultHTTPWriteTimeout, IdleTimeout: defaultHTTPIdleTimeout}, MaxRequestBodyBytes: defaultHTTPMaxRequestBodyBytes, RateLimit: httpapi.RateLimitConfig{ReadRequestsPerSecond: defaultPaymentReadRateLimitRequestsPerSecond, ReadBurst: defaultPaymentReadRateLimitBurst, WriteRequestsPerSecond: defaultPaymentWriteRateLimitRequestsPerSecond, WriteBurst: defaultPaymentWriteRateLimitBurst}, PaymentCommandTimeout: defaultPaymentCommandTimeout, PaymentReadTimeout: defaultPaymentReadTimeout},
		Metrics:  MetricsConfig{ServerConfig: ServerConfig{Addr: defaultMetricsAddr, ReadHeaderTimeout: defaultMetricsReadHeaderTimeout, ReadTimeout: defaultMetricsReadTimeout, WriteTimeout: defaultMetricsWriteTimeout, IdleTimeout: defaultMetricsIdleTimeout}},
		Payment:  PaymentConfig{FingerprintSecret: validFingerprintSecret, IdempotencyClaimStuckAfter: defaultIdempotencyClaimStuckAfter},
		Auth: AuthConfig{HMACKey: []byte(validCredentialKey), Credentials: []serviceauth.Credential{
			{Digest: serviceauth.Digest([]byte(validCredentialKey), "test-credential"), Scopes: []serviceauth.Scope{serviceauth.ScopePaymentsRead, serviceauth.ScopePaymentsWrite}},
		}},
		MockBank: MockBankConfig{BaseURL: validMockBankBaseURL, Timeout: defaultMockBankTimeout, InitialAttemptTimeout: defaultMockBankInitialAttemptTimeout, RetryDelay: defaultMockBankRetryDelay, RetryAttemptTimeout: defaultMockBankRetryAttemptTimeout, ConnectTimeout: defaultMockBankConnectTimeout, TLSHandshakeTimeout: defaultMockBankTLSHandshakeTimeout, ResponseHeaderTimeout: defaultMockBankResponseHeaderTimeout, IdleConnectionTimeout: defaultMockBankIdleConnectionTimeout},
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config)
		wantErr string
	}{
		{"valid", func(*config) {}, ""},
		{"public listener address", func(c *config) { c.HTTP.Addr = "invalid" }, "ADDR must be a host:port address"},
		{"metrics listener address", func(c *config) { c.Metrics.Addr = "" }, "METRICS_ADDR is required"},
		{"metrics listener empty port", func(c *config) { c.Metrics.Addr = "localhost:" }, "METRICS_ADDR must be a host:port address"},
		{"metrics listener port range", func(c *config) { c.Metrics.Addr = ":65536" }, "METRICS_ADDR must use a port between 1 and 65535"},
		{"shared listener address", func(c *config) { c.Metrics.Addr = c.HTTP.Addr }, "METRICS_ADDR must differ from ADDR"},
		{"database URL", func(c *config) { c.Database.URL = "" }, "DATABASE_URL is required"},
		{"pool size", func(c *config) { c.Database.MaxOpenConnections = 0 }, "DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer"},
		{"pool relationship", func(c *config) { c.Database.MaxIdleConnections = c.Database.MaxOpenConnections + 1 }, "DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS"},
		{"payment secret", func(c *config) { c.Payment.FingerprintSecret = "" }, "FINGERPRINT_SECRET is required"},
		{"read rate", func(c *config) { c.HTTP.RateLimit.ReadRequestsPerSecond = 0 }, "RATE_LIMIT_READ_REQUESTS_PER_SECOND must be a positive integer"},
		{"read burst", func(c *config) { c.HTTP.RateLimit.ReadBurst = 0 }, "RATE_LIMIT_READ_BURST must be a positive integer"},
		{"write rate", func(c *config) { c.HTTP.RateLimit.WriteRequestsPerSecond = 0 }, "RATE_LIMIT_WRITE_REQUESTS_PER_SECOND must be a positive integer"},
		{"write burst", func(c *config) { c.HTTP.RateLimit.WriteBurst = 0 }, "RATE_LIMIT_WRITE_BURST must be a positive integer"},
		{"service credentials", func(c *config) { c.Auth.Credentials = nil }, "service credential configuration is invalid"},
		{"mock bank initial attempt", func(c *config) { c.MockBank.InitialAttemptTimeout = 0 }, "MOCK_BANK_INITIAL_ATTEMPT_TIMEOUT must be a positive duration"},
		{"mock bank timeout", func(c *config) { c.MockBank.Timeout = 0 }, "MOCK_BANK_TIMEOUT must be a positive duration"},
		{"idempotency claim stuck-after budget", func(c *config) { c.Payment.IdempotencyClaimStuckAfter = c.HTTP.PaymentCommandTimeout }, "IDEMPOTENCY_CLAIM_STUCK_AFTER must exceed PAYMENT_COMMAND_TIMEOUT"},
		{"mock bank timeout budget", func(c *config) { c.MockBank.Timeout = c.HTTP.PaymentCommandTimeout }, "MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT"},
		{"mock bank retry delay", func(c *config) { c.MockBank.RetryDelay = 0 }, "MOCK_BANK_RETRY_DELAY must be a positive duration"},
		{"mock bank retry attempt", func(c *config) { c.MockBank.RetryAttemptTimeout = 0 }, "MOCK_BANK_RETRY_ATTEMPT_TIMEOUT must be a positive duration"},
		{"mock bank retry budget", func(c *config) { c.MockBank.RetryAttemptTimeout = c.HTTP.PaymentCommandTimeout }, "Mock Bank retry budget must leave time within PAYMENT_COMMAND_TIMEOUT"},
		{"HTTP write budget", func(c *config) { c.HTTP.WriteTimeout = c.HTTP.PaymentCommandTimeout }, "HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT"},
		{"metrics read timeout", func(c *config) { c.Metrics.ReadTimeout = 0 }, "METRICS_READ_TIMEOUT must be a positive duration"},
		{"log level", func(c *config) { c.Runtime.LogLevel = "verbose" }, "LOG_LEVEL must be one of debug, info, warn, or error"},
		{"idempotency replay cleanup interval", func(c *config) { c.Runtime.IdempotencyReplayCleanupInterval = 0 }, "IDEMPOTENCY_REPLAY_CLEANUP_INTERVAL must be a positive duration"},
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

func TestLoadConfigUsesDefaults(t *testing.T) {
	setRequiredConfigEnv(t)
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, defaultHTTPAddr, cfg.HTTP.Addr)
	assert.Equal(t, defaultMetricsAddr, cfg.Metrics.Addr)
	assert.Equal(t, ServerConfig{Addr: defaultMetricsAddr, ReadHeaderTimeout: defaultMetricsReadHeaderTimeout, ReadTimeout: defaultMetricsReadTimeout, WriteTimeout: defaultMetricsWriteTimeout, IdleTimeout: defaultMetricsIdleTimeout}, cfg.Metrics.ServerConfig)
	assert.Equal(t, validDatabaseURL, cfg.Database.URL)
	assert.Equal(t, defaultDatabaseMaxOpenConnections, cfg.Database.MaxOpenConnections)
	assert.Equal(t, defaultDatabaseConnectionMaxIdleTime, cfg.Database.ConnectionMaxIdleTime)
	assert.Equal(t, defaultPaymentCommandTimeout, cfg.HTTP.PaymentCommandTimeout)
	assert.Equal(t, httpapi.RateLimitConfig{ReadRequestsPerSecond: defaultPaymentReadRateLimitRequestsPerSecond, ReadBurst: defaultPaymentReadRateLimitBurst, WriteRequestsPerSecond: defaultPaymentWriteRateLimitRequestsPerSecond, WriteBurst: defaultPaymentWriteRateLimitBurst}, cfg.HTTP.RateLimit)
	assert.Equal(t, defaultMockBankInitialAttemptTimeout, cfg.MockBank.InitialAttemptTimeout)
	assert.Equal(t, defaultMockBankRetryDelay, cfg.MockBank.RetryDelay)
	assert.Equal(t, defaultMockBankRetryAttemptTimeout, cfg.MockBank.RetryAttemptTimeout)
	assert.Equal(t, defaultLogLevel, cfg.Runtime.LogLevel)
	assert.Equal(t, defaultShutdownTimeout, cfg.Runtime.ShutdownTimeout)
	assert.Equal(t, defaultIdempotencyReplayCleanupInterval, cfg.Runtime.IdempotencyReplayCleanupInterval)
}

func TestLoadConfigAllowsComponentConfiguration(t *testing.T) {
	setRequiredConfigEnv(t)
	t.Setenv("DATABASE_MAX_OPEN_CONNECTIONS", "20")
	t.Setenv("DATABASE_MAX_IDLE_CONNECTIONS", "8")
	t.Setenv("DATABASE_CONNECTION_MAX_LIFETIME", "45m")
	t.Setenv("PAYMENT_COMMAND_TIMEOUT", "12s")
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
	assert.Equal(t, 20, cfg.Database.MaxOpenConnections)
	assert.Equal(t, 8, cfg.Database.MaxIdleConnections)
	assert.Equal(t, 45*time.Minute, cfg.Database.ConnectionMaxLifetime)
	assert.Equal(t, 12*time.Second, cfg.HTTP.PaymentCommandTimeout)
	assert.Equal(t, 3*time.Second, cfg.MockBank.InitialAttemptTimeout)
	assert.Equal(t, 500*time.Millisecond, cfg.MockBank.RetryDelay)
	assert.Equal(t, 6*time.Second, cfg.MockBank.RetryAttemptTimeout)
	assert.Equal(t, int64(32768), cfg.HTTP.MaxRequestBodyBytes)
	assert.Equal(t, httpapi.RateLimitConfig{ReadRequestsPerSecond: 31, ReadBurst: 61, WriteRequestsPerSecond: 6, WriteBurst: 11}, cfg.HTTP.RateLimit)
	assert.Equal(t, "127.0.0.1:9191", cfg.Metrics.Addr)
	assert.Equal(t, 4*time.Second, cfg.Metrics.ReadTimeout)
	assert.Equal(t, "debug", cfg.Runtime.LogLevel)
	assert.Equal(t, 30*time.Minute, cfg.Runtime.IdempotencyReplayCleanupInterval)
}

func TestLoadConfigRejectsMalformedValues(t *testing.T) {
	for _, tt := range []struct{ name, value string }{
		{"DATABASE_MAX_OPEN_CONNECTIONS", "many"},
		{"DATABASE_CONNECTION_MAX_IDLE_TIME", "forever"},
		{"PAYMENT_COMMAND_TIMEOUT", "forever"},
		{"MOCK_BANK_RETRY_DELAY", "later"},
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
	t.Setenv("MOCK_BANK_BASE_URL", validMockBankBaseURL)
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
