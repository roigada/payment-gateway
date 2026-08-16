package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/mockbank"
	"github.com/roigada/payment-gateway/internal/postgres"
	"github.com/roigada/payment-gateway/internal/serviceauth"
)

const (
	defaultDatabaseMaxOpenConnections                   = 10
	defaultDatabaseMaxIdleConnections                   = 5
	defaultDatabaseConnectionMaxLifetime                = 30 * time.Minute
	defaultDatabaseConnectionMaxIdleTime                = 5 * time.Minute
	defaultDatabaseStartupTimeout                       = 5 * time.Second
	defaultIdempotencyClaimStuckAfter                   = 5 * time.Minute
	defaultIdempotencyReplayCleanupInterval             = time.Hour
	defaultLogLevel                                     = "info"
	defaultShutdownTimeout                              = 30 * time.Second
	defaultPaymentCommandTimeout                        = 10 * time.Second
	defaultPaymentReadTimeout                           = 3 * time.Second
	defaultReadinessCheckTimeout                        = 2 * time.Second
	defaultMockBankInitialAttemptTimeout                = 2 * time.Second
	defaultMockBankRetryDelay                           = 250 * time.Millisecond
	defaultMockBankRetryAttemptTimeout                  = 5 * time.Second
	defaultMockBankTimeout                              = 7 * time.Second
	defaultHTTPReadHeaderTimeout                        = 5 * time.Second
	defaultHTTPAddr                                     = ":8080"
	defaultMetricsAddr                                  = ":9091"
	defaultHTTPReadTimeout                              = 15 * time.Second
	defaultHTTPWriteTimeout                             = 15 * time.Second
	defaultHTTPIdleTimeout                              = 60 * time.Second
	defaultMetricsReadHeaderTimeout                     = 5 * time.Second
	defaultMetricsReadTimeout                           = 5 * time.Second
	defaultMetricsWriteTimeout                          = 10 * time.Second
	defaultMetricsIdleTimeout                           = 30 * time.Second
	defaultHTTPMaxRequestBodyBytes                int64 = 64 * 1024
	defaultMockBankConnectTimeout                       = 2 * time.Second
	defaultMockBankTLSHandshakeTimeout                  = 2 * time.Second
	defaultMockBankResponseHeaderTimeout                = 6 * time.Second
	defaultMockBankIdleConnectionTimeout                = 60 * time.Second
	defaultPaymentReadRateLimitRequestsPerSecond        = 30
	defaultPaymentReadRateLimitBurst                    = 60
	defaultPaymentWriteRateLimitRequestsPerSecond       = 5
	defaultPaymentWriteRateLimitBurst                   = 10
)

const paymentCommandCompletionReserve = time.Second

type config struct {
	LogLevel                         string
	ShutdownTimeout                  time.Duration
	IdempotencyReplayCleanupInterval time.Duration
	DatabaseURL                      string
	DatabaseMaxOpenConnections       int
	DatabaseMaxIdleConnections       int
	DatabaseConnectionMaxLifetime    time.Duration
	DatabaseConnectionMaxIdleTime    time.Duration
	DatabaseStartupTimeout           time.Duration
	HTTPAddr                         string
	HTTPReadHeaderTimeout            time.Duration
	HTTPReadTimeout                  time.Duration
	HTTPWriteTimeout                 time.Duration
	HTTPIdleTimeout                  time.Duration
	HTTPMaxRequestBodyBytes          int64
	RateLimitReadRequestsPerSecond   int
	RateLimitReadBurst               int
	RateLimitWriteRequestsPerSecond  int
	RateLimitWriteBurst              int
	PaymentCommandTimeout            time.Duration
	PaymentReadTimeout               time.Duration
	ReadinessTimeout                 time.Duration
	MetricsAddr                      string
	MetricsReadHeaderTimeout         time.Duration
	MetricsReadTimeout               time.Duration
	MetricsWriteTimeout              time.Duration
	MetricsIdleTimeout               time.Duration
	FingerprintSecret                string
	IdempotencyClaimStuckAfter       time.Duration
	ServiceCredentialHMACKey         []byte
	ServiceCredentials               []serviceauth.Credential
	MockBankBaseURL                  url.URL
	MockBankTimeout                  time.Duration
	MockBankInitialAttemptTimeout    time.Duration
	MockBankRetryDelay               time.Duration
	MockBankRetryAttemptTimeout      time.Duration
	MockBankConnectTimeout           time.Duration
	MockBankTLSHandshakeTimeout      time.Duration
	MockBankResponseHeaderTimeout    time.Duration
	MockBankIdleConnectionTimeout    time.Duration
}

func (cfg config) postgresConfig() postgres.Config {
	return postgres.Config{URL: cfg.DatabaseURL, MaxOpenConnections: cfg.DatabaseMaxOpenConnections, MaxIdleConnections: cfg.DatabaseMaxIdleConnections, ConnectionMaxLifetime: cfg.DatabaseConnectionMaxLifetime, ConnectionMaxIdleTime: cfg.DatabaseConnectionMaxIdleTime}
}

func (cfg config) mockBankConfig() mockbank.Config {
	return mockbank.Config{BaseURL: cfg.MockBankBaseURL, Timeout: cfg.MockBankTimeout, InitialAttemptTimeout: cfg.MockBankInitialAttemptTimeout, RetryDelay: cfg.MockBankRetryDelay, RetryAttemptTimeout: cfg.MockBankRetryAttemptTimeout, ConnectTimeout: cfg.MockBankConnectTimeout, TLSHandshakeTimeout: cfg.MockBankTLSHandshakeTimeout, ResponseHeaderTimeout: cfg.MockBankResponseHeaderTimeout, IdleConnectionTimeout: cfg.MockBankIdleConnectionTimeout}
}

func (cfg config) handlerConfig() httpapi.HandlerConfig {
	return httpapi.HandlerConfig{PaymentCommandTimeout: cfg.PaymentCommandTimeout, PaymentReadTimeout: cfg.PaymentReadTimeout, ReadinessTimeout: cfg.ReadinessTimeout, MaxRequestBodyBytes: cfg.HTTPMaxRequestBodyBytes, RateLimit: cfg.rateLimitConfig()}
}

func (cfg config) rateLimitConfig() httpapi.RateLimitConfig {
	return httpapi.RateLimitConfig{ReadRequestsPerSecond: cfg.RateLimitReadRequestsPerSecond, ReadBurst: cfg.RateLimitReadBurst, WriteRequestsPerSecond: cfg.RateLimitWriteRequestsPerSecond, WriteBurst: cfg.RateLimitWriteBurst}
}

func (cfg config) publicServer(handler http.Handler) *http.Server {
	return newHTTPServer(handler, cfg.HTTPAddr, cfg.HTTPReadHeaderTimeout, cfg.HTTPReadTimeout, cfg.HTTPWriteTimeout, cfg.HTTPIdleTimeout)
}

func (cfg config) metricsServer(handler http.Handler) *http.Server {
	return newHTTPServer(handler, cfg.MetricsAddr, cfg.MetricsReadHeaderTimeout, cfg.MetricsReadTimeout, cfg.MetricsWriteTimeout, cfg.MetricsIdleTimeout)
}

func loadConfig() (config, error) {
	serviceCredentialHMACKey, err := envBase64("SERVICE_CREDENTIAL_HMAC_KEY")
	if err != nil {
		return config{}, err
	}
	serviceCredentials, err := parseServiceCredentials(os.Getenv("ORDER_SERVICE_CREDENTIALS"))
	if err != nil {
		return config{}, err
	}
	databaseMaxOpenConnections, err := envInt("DATABASE_MAX_OPEN_CONNECTIONS", defaultDatabaseMaxOpenConnections)
	if err != nil {
		return config{}, err
	}
	databaseMaxIdleConnections, err := envInt("DATABASE_MAX_IDLE_CONNECTIONS", defaultDatabaseMaxIdleConnections)
	if err != nil {
		return config{}, err
	}
	databaseConnectionMaxLifetime, err := envDuration("DATABASE_CONNECTION_MAX_LIFETIME", defaultDatabaseConnectionMaxLifetime)
	if err != nil {
		return config{}, err
	}
	databaseConnectionMaxIdleTime, err := envDuration("DATABASE_CONNECTION_MAX_IDLE_TIME", defaultDatabaseConnectionMaxIdleTime)
	if err != nil {
		return config{}, err
	}
	databaseStartupTimeout, err := envDuration("DATABASE_STARTUP_TIMEOUT", defaultDatabaseStartupTimeout)
	if err != nil {
		return config{}, err
	}
	idempotencyClaimStuckAfter, err := envDuration("IDEMPOTENCY_CLAIM_STUCK_AFTER", defaultIdempotencyClaimStuckAfter)
	if err != nil {
		return config{}, err
	}
	idempotencyReplayCleanupInterval, err := envDuration("IDEMPOTENCY_REPLAY_CLEANUP_INTERVAL", defaultIdempotencyReplayCleanupInterval)
	if err != nil {
		return config{}, err
	}
	paymentCommandTimeout, err := envDuration("PAYMENT_COMMAND_TIMEOUT", defaultPaymentCommandTimeout)
	if err != nil {
		return config{}, err
	}
	paymentReadTimeout, err := envDuration("PAYMENT_READ_TIMEOUT", defaultPaymentReadTimeout)
	if err != nil {
		return config{}, err
	}
	readinessTimeout, err := envDuration("READINESS_CHECK_TIMEOUT", defaultReadinessCheckTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankInitialAttemptTimeout, err := envDuration("MOCK_BANK_INITIAL_ATTEMPT_TIMEOUT", defaultMockBankInitialAttemptTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankRetryDelay, err := envDuration("MOCK_BANK_RETRY_DELAY", defaultMockBankRetryDelay)
	if err != nil {
		return config{}, err
	}
	mockBankRetryAttemptTimeout, err := envDuration("MOCK_BANK_RETRY_ATTEMPT_TIMEOUT", defaultMockBankRetryAttemptTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankTimeout, err := envDuration("MOCK_BANK_TIMEOUT", defaultMockBankTimeout)
	if err != nil {
		return config{}, err
	}
	httpReadHeaderTimeout, err := envDuration("HTTP_READ_HEADER_TIMEOUT", defaultHTTPReadHeaderTimeout)
	if err != nil {
		return config{}, err
	}
	httpReadTimeout, err := envDuration("HTTP_READ_TIMEOUT", defaultHTTPReadTimeout)
	if err != nil {
		return config{}, err
	}
	httpWriteTimeout, err := envDuration("HTTP_WRITE_TIMEOUT", defaultHTTPWriteTimeout)
	if err != nil {
		return config{}, err
	}
	httpIdleTimeout, err := envDuration("HTTP_IDLE_TIMEOUT", defaultHTTPIdleTimeout)
	if err != nil {
		return config{}, err
	}
	metricsReadHeaderTimeout, err := envDuration("METRICS_READ_HEADER_TIMEOUT", defaultMetricsReadHeaderTimeout)
	if err != nil {
		return config{}, err
	}
	metricsReadTimeout, err := envDuration("METRICS_READ_TIMEOUT", defaultMetricsReadTimeout)
	if err != nil {
		return config{}, err
	}
	metricsWriteTimeout, err := envDuration("METRICS_WRITE_TIMEOUT", defaultMetricsWriteTimeout)
	if err != nil {
		return config{}, err
	}
	metricsIdleTimeout, err := envDuration("METRICS_IDLE_TIMEOUT", defaultMetricsIdleTimeout)
	if err != nil {
		return config{}, err
	}
	httpMaxRequestBodyBytes, err := envInt64("HTTP_MAX_REQUEST_BODY_BYTES", defaultHTTPMaxRequestBodyBytes)
	if err != nil {
		return config{}, err
	}
	readRateLimitRequestsPerSecond, err := envInt("RATE_LIMIT_READ_REQUESTS_PER_SECOND", defaultPaymentReadRateLimitRequestsPerSecond)
	if err != nil {
		return config{}, err
	}
	readRateLimitBurst, err := envInt("RATE_LIMIT_READ_BURST", defaultPaymentReadRateLimitBurst)
	if err != nil {
		return config{}, err
	}
	writeRateLimitRequestsPerSecond, err := envInt("RATE_LIMIT_WRITE_REQUESTS_PER_SECOND", defaultPaymentWriteRateLimitRequestsPerSecond)
	if err != nil {
		return config{}, err
	}
	writeRateLimitBurst, err := envInt("RATE_LIMIT_WRITE_BURST", defaultPaymentWriteRateLimitBurst)
	if err != nil {
		return config{}, err
	}
	mockBankConnectTimeout, err := envDuration("MOCK_BANK_CONNECT_TIMEOUT", defaultMockBankConnectTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankTLSHandshakeTimeout, err := envDuration("MOCK_BANK_TLS_HANDSHAKE_TIMEOUT", defaultMockBankTLSHandshakeTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankResponseHeaderTimeout, err := envDuration("MOCK_BANK_RESPONSE_HEADER_TIMEOUT", defaultMockBankResponseHeaderTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankIdleConnectionTimeout, err := envDuration("MOCK_BANK_IDLE_CONNECTION_TIMEOUT", defaultMockBankIdleConnectionTimeout)
	if err != nil {
		return config{}, err
	}
	shutdownTimeout, err := envDuration("SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return config{}, err
	}
	mockBankBaseURL, err := envURL("MOCK_BANK_BASE_URL")
	if err != nil {
		return config{}, err
	}

	cfg := config{
		LogLevel: envString("LOG_LEVEL", defaultLogLevel), ShutdownTimeout: shutdownTimeout, IdempotencyReplayCleanupInterval: idempotencyReplayCleanupInterval,
		DatabaseURL: os.Getenv("DATABASE_URL"), DatabaseMaxOpenConnections: databaseMaxOpenConnections, DatabaseMaxIdleConnections: databaseMaxIdleConnections, DatabaseConnectionMaxLifetime: databaseConnectionMaxLifetime, DatabaseConnectionMaxIdleTime: databaseConnectionMaxIdleTime, DatabaseStartupTimeout: databaseStartupTimeout,
		HTTPAddr: envString("ADDR", defaultHTTPAddr), HTTPReadHeaderTimeout: httpReadHeaderTimeout, HTTPReadTimeout: httpReadTimeout, HTTPWriteTimeout: httpWriteTimeout, HTTPIdleTimeout: httpIdleTimeout, HTTPMaxRequestBodyBytes: httpMaxRequestBodyBytes, RateLimitReadRequestsPerSecond: readRateLimitRequestsPerSecond, RateLimitReadBurst: readRateLimitBurst, RateLimitWriteRequestsPerSecond: writeRateLimitRequestsPerSecond, RateLimitWriteBurst: writeRateLimitBurst, PaymentCommandTimeout: paymentCommandTimeout, PaymentReadTimeout: paymentReadTimeout, ReadinessTimeout: readinessTimeout,
		MetricsAddr: envString("METRICS_ADDR", defaultMetricsAddr), MetricsReadHeaderTimeout: metricsReadHeaderTimeout, MetricsReadTimeout: metricsReadTimeout, MetricsWriteTimeout: metricsWriteTimeout, MetricsIdleTimeout: metricsIdleTimeout,
		FingerprintSecret: os.Getenv("FINGERPRINT_SECRET"), IdempotencyClaimStuckAfter: idempotencyClaimStuckAfter, ServiceCredentialHMACKey: serviceCredentialHMACKey, ServiceCredentials: serviceCredentials,
		MockBankBaseURL: mockBankBaseURL, MockBankTimeout: mockBankTimeout, MockBankInitialAttemptTimeout: mockBankInitialAttemptTimeout, MockBankRetryDelay: mockBankRetryDelay, MockBankRetryAttemptTimeout: mockBankRetryAttemptTimeout, MockBankConnectTimeout: mockBankConnectTimeout, MockBankTLSHandshakeTimeout: mockBankTLSHandshakeTimeout, MockBankResponseHeaderTimeout: mockBankResponseHeaderTimeout, MockBankIdleConnectionTimeout: mockBankIdleConnectionTimeout,
	}
	return cfg, nil
}

func (cfg config) validate() error {
	if err := validateListenerAddr("ADDR", cfg.HTTPAddr); err != nil {
		return err
	}
	if err := validateListenerAddr("METRICS_ADDR", cfg.MetricsAddr); err != nil {
		return err
	}
	if cfg.HTTPAddr == cfg.MetricsAddr {
		return fmt.Errorf("METRICS_ADDR must differ from ADDR")
	}
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.DatabaseMaxOpenConnections <= 0 {
		return fmt.Errorf("DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer")
	}
	if cfg.DatabaseMaxIdleConnections < 0 {
		return fmt.Errorf("DATABASE_MAX_IDLE_CONNECTIONS must be a non-negative integer")
	}
	if cfg.DatabaseMaxIdleConnections > cfg.DatabaseMaxOpenConnections {
		return fmt.Errorf("DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS")
	}
	if cfg.DatabaseConnectionMaxLifetime <= 0 {
		return fmt.Errorf("DATABASE_CONNECTION_MAX_LIFETIME must be a positive duration")
	}
	if cfg.MockBankBaseURL.Scheme == "" || cfg.MockBankBaseURL.Host == "" {
		return fmt.Errorf("MOCK_BANK_BASE_URL must be an absolute URL")
	}
	if cfg.FingerprintSecret == "" {
		return fmt.Errorf("FINGERPRINT_SECRET is required")
	}
	if cfg.IdempotencyClaimStuckAfter <= 0 {
		return fmt.Errorf("IDEMPOTENCY_CLAIM_STUCK_AFTER must be a positive duration")
	}
	if cfg.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}
	if cfg.IdempotencyReplayCleanupInterval <= 0 {
		return fmt.Errorf("IDEMPOTENCY_REPLAY_CLEANUP_INTERVAL must be a positive duration")
	}
	if cfg.DatabaseConnectionMaxIdleTime <= 0 {
		return fmt.Errorf("DATABASE_CONNECTION_MAX_IDLE_TIME must be a positive duration")
	}
	if cfg.DatabaseStartupTimeout <= 0 {
		return fmt.Errorf("DATABASE_STARTUP_TIMEOUT must be a positive duration")
	}
	if cfg.LogLevel != "debug" && cfg.LogLevel != "info" && cfg.LogLevel != "warn" && cfg.LogLevel != "error" {
		return fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, or error")
	}
	if cfg.PaymentCommandTimeout <= 0 {
		return fmt.Errorf("PAYMENT_COMMAND_TIMEOUT must be a positive duration")
	}
	if cfg.IdempotencyClaimStuckAfter <= cfg.PaymentCommandTimeout {
		return fmt.Errorf("IDEMPOTENCY_CLAIM_STUCK_AFTER must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.PaymentReadTimeout <= 0 {
		return fmt.Errorf("PAYMENT_READ_TIMEOUT must be a positive duration")
	}
	if cfg.ReadinessTimeout <= 0 {
		return fmt.Errorf("READINESS_CHECK_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankInitialAttemptTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_INITIAL_ATTEMPT_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankTimeout >= cfg.PaymentCommandTimeout {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.MockBankRetryDelay <= 0 {
		return fmt.Errorf("MOCK_BANK_RETRY_DELAY must be a positive duration")
	}
	if cfg.MockBankRetryAttemptTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_RETRY_ATTEMPT_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankInitialAttemptTimeout+cfg.MockBankRetryDelay+cfg.MockBankRetryAttemptTimeout+paymentCommandCompletionReserve >= cfg.PaymentCommandTimeout {
		return fmt.Errorf("Mock Bank retry budget must leave time within PAYMENT_COMMAND_TIMEOUT to persist the final payment outcome")
	}
	if err := validateServerTimeouts("HTTP", cfg.HTTPReadHeaderTimeout, cfg.HTTPReadTimeout, cfg.HTTPWriteTimeout, cfg.HTTPIdleTimeout); err != nil {
		return err
	}
	if cfg.HTTPReadTimeout <= cfg.PaymentCommandTimeout {
		return fmt.Errorf("HTTP_READ_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.HTTPWriteTimeout <= cfg.PaymentCommandTimeout {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if err := validateServerTimeouts("METRICS", cfg.MetricsReadHeaderTimeout, cfg.MetricsReadTimeout, cfg.MetricsWriteTimeout, cfg.MetricsIdleTimeout); err != nil {
		return err
	}
	if cfg.HTTPMaxRequestBodyBytes <= 0 {
		return fmt.Errorf("HTTP_MAX_REQUEST_BODY_BYTES must be a positive integer")
	}
	if cfg.RateLimitReadRequestsPerSecond <= 0 {
		return fmt.Errorf("RATE_LIMIT_READ_REQUESTS_PER_SECOND must be a positive integer")
	}
	if cfg.RateLimitReadBurst <= 0 {
		return fmt.Errorf("RATE_LIMIT_READ_BURST must be a positive integer")
	}
	if cfg.RateLimitWriteRequestsPerSecond <= 0 {
		return fmt.Errorf("RATE_LIMIT_WRITE_REQUESTS_PER_SECOND must be a positive integer")
	}
	if cfg.RateLimitWriteBurst <= 0 {
		return fmt.Errorf("RATE_LIMIT_WRITE_BURST must be a positive integer")
	}
	if cfg.MockBankConnectTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_CONNECT_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankTLSHandshakeTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_TLS_HANDSHAKE_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankResponseHeaderTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_RESPONSE_HEADER_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankIdleConnectionTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_IDLE_CONNECTION_TIMEOUT must be a positive duration")
	}

	return nil
}

func validateServerTimeouts(prefix string, readHeaderTimeout, readTimeout, writeTimeout, idleTimeout time.Duration) error {
	if readHeaderTimeout <= 0 {
		return fmt.Errorf("%s_READ_HEADER_TIMEOUT must be a positive duration", prefix)
	}
	if readTimeout <= 0 {
		return fmt.Errorf("%s_READ_TIMEOUT must be a positive duration", prefix)
	}
	if writeTimeout <= 0 {
		return fmt.Errorf("%s_WRITE_TIMEOUT must be a positive duration", prefix)
	}
	if idleTimeout <= 0 {
		return fmt.Errorf("%s_IDLE_TIMEOUT must be a positive duration", prefix)
	}
	return nil
}

func validateListenerAddr(name, addr string) error {
	if addr == "" {
		return fmt.Errorf("%s is required", name)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("%s must be a host:port address", name)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("%s must use a port between 1 and 65535", name)
	}
	return nil
}

func envBase64(name string) ([]byte, error) {
	value := os.Getenv(name)
	if value == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64url-encoded", name)
	}
	return decoded, nil
}

func envURL(name string) (url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return url.URL{}, fmt.Errorf("%s must be a valid URL", name)
	}
	return *parsed, nil
}

// parseServiceCredentials accepts comma-separated digest=scope+scope entries.
// Digests are base64url HMAC-SHA-256 values; scopes are payments:read or payments:write.
func parseServiceCredentials(value string) ([]serviceauth.Credential, error) {
	if value == "" {
		return nil, fmt.Errorf("ORDER_SERVICE_CREDENTIALS is required")
	}
	entries := strings.Split(value, ",")
	credentials := make([]serviceauth.Credential, 0, len(entries))
	for _, entry := range entries {
		digest, scopes, ok := strings.Cut(entry, "=")
		if !ok || digest == "" || scopes == "" {
			return nil, fmt.Errorf("ORDER_SERVICE_CREDENTIALS must contain digest=scope+scope entries")
		}
		scopeValues := strings.Split(scopes, "+")
		credentialScopes := make([]serviceauth.Scope, len(scopeValues))
		for i, scope := range scopeValues {
			credentialScopes[i] = serviceauth.Scope(scope)
		}
		credentials = append(credentials, serviceauth.Credential{Digest: digest, Scopes: credentialScopes})
	}
	return credentials, nil
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}

	return parsed, nil
}

func envString(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func envInt64(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return parsed, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration", name)
	}

	return parsed, nil
}
