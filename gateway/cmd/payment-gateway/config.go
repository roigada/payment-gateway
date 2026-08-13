package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/httpapi"
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
	defaultIdempotencyReplayWindow                      = 24 * time.Hour
	defaultIdempotencyReplayCleanupInterval             = time.Hour
	defaultLogLevel                                     = "info"
	defaultShutdownTimeout                              = 30 * time.Second
	defaultPaymentCommandTimeout                        = 10 * time.Second
	defaultPaymentReadTimeout                           = 3 * time.Second
	readinessCheckTimeout                               = 2 * time.Second
	defaultMockBankInitialAttemptTimeout                = 2 * time.Second
	defaultMockBankRetryDelay                           = 250 * time.Millisecond
	defaultMockBankRetryAttemptTimeout                  = 5 * time.Second
	defaultMockBankTimeout                              = 7 * time.Second
	paymentCommandCompletionReserve                     = time.Second
	defaultHTTPReadHeaderTimeout                        = 5 * time.Second
	defaultHTTPAddr                                     = ":8080"
	defaultMetricsAddr                                  = ":9091"
	defaultHTTPReadTimeout                              = 15 * time.Second
	defaultHTTPWriteTimeout                             = 15 * time.Second
	defaultHTTPIdleTimeout                              = 60 * time.Second
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

type config struct {
	Runtime  RuntimeConfig
	Database DatabaseConfig
	HTTP     HTTPConfig
	Metrics  MetricsConfig
	Payment  PaymentConfig
	Auth     AuthConfig
	MockBank MockBankConfig
}

type RuntimeConfig struct {
	LogLevel                         string
	ShutdownTimeout                  time.Duration
	IdempotencyReplayWindow          time.Duration
	IdempotencyReplayCleanupInterval time.Duration
}

type DatabaseConfig struct {
	URL                   string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
	StartupTimeout        time.Duration
}

type HTTPConfig struct {
	Addr                string
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	MaxRequestBodyBytes int64
	RateLimit           httpapi.RateLimitConfig
}

type MetricsConfig struct {
	Addr string
}

type PaymentConfig struct {
	FingerprintSecret          string
	IdempotencyClaimStuckAfter time.Duration
	CommandTimeout             time.Duration
	ReadTimeout                time.Duration
}

type AuthConfig struct {
	HMACKey     []byte
	Credentials []serviceauth.Credential
}

type MockBankConfig struct {
	BaseURL               string
	Timeout               time.Duration
	InitialAttemptTimeout time.Duration
	RetryDelay            time.Duration
	RetryAttemptTimeout   time.Duration
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnectionTimeout time.Duration
}

type httpHandlerConfig struct {
	Payment   PaymentConfig
	MockBank  MockBankConfig
	RateLimit httpapi.RateLimitConfig
	Options   httpapi.HandlerOptions
	Auth      AuthConfig
}

func (cfg config) httpHandler() httpHandlerConfig {
	return httpHandlerConfig{
		Payment:   cfg.Payment,
		MockBank:  cfg.MockBank,
		RateLimit: cfg.HTTP.RateLimit,
		Options: httpapi.HandlerOptions{
			PaymentCommandTimeout: cfg.Payment.CommandTimeout,
			PaymentReadTimeout:    cfg.Payment.ReadTimeout,
			ReadinessTimeout:      readinessCheckTimeout,
			MaxRequestBodyBytes:   cfg.HTTP.MaxRequestBodyBytes,
		},
		Auth: cfg.Auth,
	}
}

func (cfg AuthConfig) authenticator() (*serviceauth.Authenticator, error) {
	return serviceauth.NewAuthenticator(cfg.HMACKey, cfg.Credentials)
}

func (cfg DatabaseConfig) postgresOptions() postgres.Options {
	return postgres.Options{
		URL:                   cfg.URL,
		MaxOpenConnections:    cfg.MaxOpenConnections,
		MaxIdleConnections:    cfg.MaxIdleConnections,
		ConnectionMaxLifetime: cfg.ConnectionMaxLifetime,
		ConnectionMaxIdleTime: cfg.ConnectionMaxIdleTime,
	}
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
	idempotencyReplayWindow, err := envDuration("IDEMPOTENCY_REPLAY_WINDOW", defaultIdempotencyReplayWindow)
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

	cfg := config{
		Runtime:  RuntimeConfig{LogLevel: envString("LOG_LEVEL", defaultLogLevel), ShutdownTimeout: shutdownTimeout, IdempotencyReplayWindow: idempotencyReplayWindow, IdempotencyReplayCleanupInterval: idempotencyReplayCleanupInterval},
		Database: DatabaseConfig{URL: os.Getenv("DATABASE_URL"), MaxOpenConnections: databaseMaxOpenConnections, MaxIdleConnections: databaseMaxIdleConnections, ConnectionMaxLifetime: databaseConnectionMaxLifetime, ConnectionMaxIdleTime: databaseConnectionMaxIdleTime, StartupTimeout: databaseStartupTimeout},
		HTTP:     HTTPConfig{Addr: envString("ADDR", defaultHTTPAddr), ReadHeaderTimeout: httpReadHeaderTimeout, ReadTimeout: httpReadTimeout, WriteTimeout: httpWriteTimeout, IdleTimeout: httpIdleTimeout, MaxRequestBodyBytes: httpMaxRequestBodyBytes, RateLimit: httpapi.RateLimitConfig{ReadRequestsPerSecond: readRateLimitRequestsPerSecond, ReadBurst: readRateLimitBurst, WriteRequestsPerSecond: writeRateLimitRequestsPerSecond, WriteBurst: writeRateLimitBurst}},
		Metrics:  MetricsConfig{Addr: envString("METRICS_ADDR", defaultMetricsAddr)},
		Payment:  PaymentConfig{FingerprintSecret: os.Getenv("FINGERPRINT_SECRET"), IdempotencyClaimStuckAfter: idempotencyClaimStuckAfter, CommandTimeout: paymentCommandTimeout, ReadTimeout: paymentReadTimeout},
		Auth:     AuthConfig{HMACKey: serviceCredentialHMACKey, Credentials: serviceCredentials},
		MockBank: MockBankConfig{BaseURL: os.Getenv("MOCK_BANK_BASE_URL"), Timeout: mockBankTimeout, InitialAttemptTimeout: mockBankInitialAttemptTimeout, RetryDelay: mockBankRetryDelay, RetryAttemptTimeout: mockBankRetryAttemptTimeout, ConnectTimeout: mockBankConnectTimeout, TLSHandshakeTimeout: mockBankTLSHandshakeTimeout, ResponseHeaderTimeout: mockBankResponseHeaderTimeout, IdleConnectionTimeout: mockBankIdleConnectionTimeout},
	}
	return cfg, nil
}

func (cfg config) validate() error {
	if err := validateListenerAddr("ADDR", cfg.HTTP.Addr); err != nil {
		return err
	}
	if err := validateListenerAddr("METRICS_ADDR", cfg.Metrics.Addr); err != nil {
		return err
	}
	if cfg.HTTP.Addr == cfg.Metrics.Addr {
		return fmt.Errorf("METRICS_ADDR must differ from ADDR")
	}
	if cfg.Database.URL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.Database.MaxOpenConnections <= 0 {
		return fmt.Errorf("DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer")
	}
	if cfg.Database.MaxIdleConnections < 0 {
		return fmt.Errorf("DATABASE_MAX_IDLE_CONNECTIONS must be a non-negative integer")
	}
	if cfg.Database.MaxIdleConnections > cfg.Database.MaxOpenConnections {
		return fmt.Errorf("DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS")
	}
	if cfg.Database.ConnectionMaxLifetime <= 0 {
		return fmt.Errorf("DATABASE_CONNECTION_MAX_LIFETIME must be a positive duration")
	}
	if cfg.MockBank.BaseURL == "" {
		return fmt.Errorf("MOCK_BANK_BASE_URL is required")
	}
	if cfg.Payment.FingerprintSecret == "" {
		return fmt.Errorf("FINGERPRINT_SECRET is required")
	}
	if _, err := cfg.Auth.authenticator(); err != nil {
		return fmt.Errorf("service credential configuration is invalid: %w", err)
	}
	if cfg.Payment.IdempotencyClaimStuckAfter <= 0 {
		return fmt.Errorf("IDEMPOTENCY_CLAIM_STUCK_AFTER must be a positive duration")
	}
	if cfg.Runtime.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}
	if cfg.Runtime.IdempotencyReplayWindow <= 0 {
		return fmt.Errorf("IDEMPOTENCY_REPLAY_WINDOW must be a positive duration")
	}
	if cfg.Runtime.IdempotencyReplayCleanupInterval <= 0 {
		return fmt.Errorf("IDEMPOTENCY_REPLAY_CLEANUP_INTERVAL must be a positive duration")
	}
	if cfg.Database.ConnectionMaxIdleTime <= 0 {
		return fmt.Errorf("DATABASE_CONNECTION_MAX_IDLE_TIME must be a positive duration")
	}
	if cfg.Database.StartupTimeout <= 0 {
		return fmt.Errorf("DATABASE_STARTUP_TIMEOUT must be a positive duration")
	}
	if cfg.Runtime.LogLevel != "debug" && cfg.Runtime.LogLevel != "info" && cfg.Runtime.LogLevel != "warn" && cfg.Runtime.LogLevel != "error" {
		return fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, or error")
	}
	if cfg.Payment.CommandTimeout <= 0 {
		return fmt.Errorf("PAYMENT_COMMAND_TIMEOUT must be a positive duration")
	}
	if cfg.Payment.IdempotencyClaimStuckAfter <= cfg.Payment.CommandTimeout {
		return fmt.Errorf("IDEMPOTENCY_CLAIM_STUCK_AFTER must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.Payment.ReadTimeout <= 0 {
		return fmt.Errorf("PAYMENT_READ_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.InitialAttemptTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_INITIAL_ATTEMPT_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.Timeout <= 0 {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.Timeout >= cfg.Payment.CommandTimeout {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.MockBank.RetryDelay <= 0 {
		return fmt.Errorf("MOCK_BANK_RETRY_DELAY must be a positive duration")
	}
	if cfg.MockBank.RetryAttemptTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_RETRY_ATTEMPT_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.InitialAttemptTimeout+cfg.MockBank.RetryDelay+cfg.MockBank.RetryAttemptTimeout+paymentCommandCompletionReserve >= cfg.Payment.CommandTimeout {
		return fmt.Errorf("Mock Bank retry budget must leave time within PAYMENT_COMMAND_TIMEOUT to persist the final payment outcome")
	}
	if cfg.HTTP.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be a positive duration")
	}
	if cfg.HTTP.ReadTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_TIMEOUT must be a positive duration")
	}
	if cfg.HTTP.WriteTimeout <= 0 {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must be a positive duration")
	}
	if cfg.HTTP.ReadTimeout <= cfg.Payment.CommandTimeout {
		return fmt.Errorf("HTTP_READ_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.HTTP.WriteTimeout <= cfg.Payment.CommandTimeout {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.HTTP.IdleTimeout <= 0 {
		return fmt.Errorf("HTTP_IDLE_TIMEOUT must be a positive duration")
	}
	if cfg.HTTP.MaxRequestBodyBytes <= 0 {
		return fmt.Errorf("HTTP_MAX_REQUEST_BODY_BYTES must be a positive integer")
	}
	if cfg.HTTP.RateLimit.ReadRequestsPerSecond <= 0 {
		return fmt.Errorf("RATE_LIMIT_READ_REQUESTS_PER_SECOND must be a positive integer")
	}
	if cfg.HTTP.RateLimit.ReadBurst <= 0 {
		return fmt.Errorf("RATE_LIMIT_READ_BURST must be a positive integer")
	}
	if cfg.HTTP.RateLimit.WriteRequestsPerSecond <= 0 {
		return fmt.Errorf("RATE_LIMIT_WRITE_REQUESTS_PER_SECOND must be a positive integer")
	}
	if cfg.HTTP.RateLimit.WriteBurst <= 0 {
		return fmt.Errorf("RATE_LIMIT_WRITE_BURST must be a positive integer")
	}
	if cfg.MockBank.ConnectTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_CONNECT_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.TLSHandshakeTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_TLS_HANDSHAKE_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.ResponseHeaderTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_RESPONSE_HEADER_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.IdleConnectionTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_IDLE_CONNECTION_TIMEOUT must be a positive duration")
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
