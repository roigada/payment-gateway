package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	defaultDatabaseMaxOpenConnections          = 10
	defaultDatabaseMaxIdleConnections          = 5
	defaultDatabaseConnectionMaxLifetime       = 30 * time.Minute
	defaultDatabaseConnectionMaxIdleTime       = 5 * time.Minute
	defaultDatabaseStartupTimeout              = 5 * time.Second
	defaultIdempotencyClaimStuckAfter          = app.DefaultIdempotencyClaimStuckAfter
	defaultShutdownTimeout                     = 30 * time.Second
	defaultPaymentCommandTimeout               = 10 * time.Second
	defaultPaymentReadTimeout                  = 3 * time.Second
	defaultReadyTimeout                        = 2 * time.Second
	defaultMockBankTimeout                     = 7 * time.Second
	defaultHTTPReadHeaderTimeout               = 5 * time.Second
	defaultHTTPReadTimeout                     = 15 * time.Second
	defaultHTTPWriteTimeout                    = 15 * time.Second
	defaultHTTPIdleTimeout                     = 60 * time.Second
	defaultHTTPMaxRequestBodyBytes       int64 = 64 * 1024
	defaultMockBankConnectTimeout              = 2 * time.Second
	defaultMockBankTLSHandshakeTimeout         = 2 * time.Second
	defaultMockBankResponseHeaderTimeout       = 6 * time.Second
	defaultMockBankIdleConnectionTimeout       = 60 * time.Second
)

type config struct {
	Addr                          string
	DatabaseURL                   string
	DatabaseMaxOpenConnections    int
	DatabaseMaxIdleConnections    int
	DatabaseConnectionMaxLifetime time.Duration
	DatabaseConnectionMaxIdleTime time.Duration
	DatabaseStartupTimeout        time.Duration
	MockBankBaseURL               string
	FingerprintSecret             string
	IdempotencyClaimStuckAfter    time.Duration
	ShutdownTimeout               time.Duration
	LogLevel                      string
	PaymentCommandTimeout         time.Duration
	PaymentReadTimeout            time.Duration
	MockBankTimeout               time.Duration
	HTTPReadHeaderTimeout         time.Duration
	HTTPReadTimeout               time.Duration
	HTTPWriteTimeout              time.Duration
	HTTPIdleTimeout               time.Duration
	HTTPMaxRequestBodyBytes       int64
	MockBankConnectTimeout        time.Duration
	MockBankTLSHandshakeTimeout   time.Duration
	MockBankResponseHeaderTimeout time.Duration
	MockBankIdleConnectionTimeout time.Duration
}

func loadConfig() (config, error) {
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
	paymentCommandTimeout, err := envDuration("PAYMENT_COMMAND_TIMEOUT", defaultPaymentCommandTimeout)
	if err != nil {
		return config{}, err
	}
	paymentReadTimeout, err := envDuration("PAYMENT_READ_TIMEOUT", defaultPaymentReadTimeout)
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
		Addr:                          os.Getenv("ADDR"),
		DatabaseURL:                   os.Getenv("DATABASE_URL"),
		DatabaseMaxOpenConnections:    databaseMaxOpenConnections,
		DatabaseMaxIdleConnections:    databaseMaxIdleConnections,
		DatabaseConnectionMaxLifetime: databaseConnectionMaxLifetime,
		DatabaseConnectionMaxIdleTime: databaseConnectionMaxIdleTime,
		DatabaseStartupTimeout:        databaseStartupTimeout,
		MockBankBaseURL:               os.Getenv("MOCK_BANK_BASE_URL"),
		FingerprintSecret:             os.Getenv("FINGERPRINT_SECRET"),
		IdempotencyClaimStuckAfter:    idempotencyClaimStuckAfter,
		ShutdownTimeout:               shutdownTimeout,
		LogLevel:                      os.Getenv("LOG_LEVEL"),
		PaymentCommandTimeout:         paymentCommandTimeout,
		PaymentReadTimeout:            paymentReadTimeout,
		MockBankTimeout:               mockBankTimeout,
		HTTPReadHeaderTimeout:         httpReadHeaderTimeout,
		HTTPReadTimeout:               httpReadTimeout,
		HTTPWriteTimeout:              httpWriteTimeout,
		HTTPIdleTimeout:               httpIdleTimeout,
		HTTPMaxRequestBodyBytes:       httpMaxRequestBodyBytes,
		MockBankConnectTimeout:        mockBankConnectTimeout,
		MockBankTLSHandshakeTimeout:   mockBankTLSHandshakeTimeout,
		MockBankResponseHeaderTimeout: mockBankResponseHeaderTimeout,
		MockBankIdleConnectionTimeout: mockBankIdleConnectionTimeout,
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	return cfg, nil
}

func (cfg config) validate() error {
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
	if cfg.MockBankBaseURL == "" {
		return fmt.Errorf("MOCK_BANK_BASE_URL is required")
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
	if cfg.PaymentReadTimeout <= 0 {
		return fmt.Errorf("PAYMENT_READ_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankTimeout <= 0 {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be a positive duration")
	}
	if cfg.MockBankTimeout >= cfg.PaymentCommandTimeout {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.HTTPReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be a positive duration")
	}
	if cfg.HTTPReadTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_TIMEOUT must be a positive duration")
	}
	if cfg.HTTPWriteTimeout <= 0 {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must be a positive duration")
	}
	if cfg.HTTPReadTimeout <= cfg.PaymentCommandTimeout {
		return fmt.Errorf("HTTP_READ_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.HTTPWriteTimeout <= cfg.PaymentCommandTimeout {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT")
	}
	if cfg.HTTPIdleTimeout <= 0 {
		return fmt.Errorf("HTTP_IDLE_TIMEOUT must be a positive duration")
	}
	if cfg.HTTPMaxRequestBodyBytes <= 0 {
		return fmt.Errorf("HTTP_MAX_REQUEST_BODY_BYTES must be a positive integer")
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
