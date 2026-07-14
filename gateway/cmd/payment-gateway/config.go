package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/httpapi"
	"github.com/roigada/payment-gateway/internal/postgres"
)

const (
	defaultDatabaseMaxOpenConnections          = 10
	defaultDatabaseMaxIdleConnections          = 5
	defaultDatabaseConnectionMaxLifetime       = 30 * time.Minute
	defaultDatabaseConnectionMaxIdleTime       = 5 * time.Minute
	defaultDatabaseStartupTimeout              = 5 * time.Second
	defaultIdempotencyClaimStuckAfter          = app.DefaultIdempotencyClaimStuckAfter
	defaultLogLevel                            = "info"
	defaultShutdownTimeout                     = 30 * time.Second
	defaultPaymentCommandTimeout               = 10 * time.Second
	defaultPaymentReadTimeout                  = 3 * time.Second
	readinessCheckTimeout                      = 2 * time.Second
	defaultMockBankTimeout                     = 7 * time.Second
	defaultHTTPReadHeaderTimeout               = 5 * time.Second
	defaultHTTPAddr                            = ":8080"
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
	Runtime  RuntimeConfig
	Database DatabaseConfig
	HTTP     HTTPConfig
	Payment  PaymentConfig
	MockBank MockBankConfig
}

type RuntimeConfig struct {
	LogLevel        string
	ShutdownTimeout time.Duration
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
}

type PaymentConfig struct {
	FingerprintSecret          string
	IdempotencyClaimStuckAfter time.Duration
	CommandTimeout             time.Duration
	ReadTimeout                time.Duration
}

type MockBankConfig struct {
	BaseURL               string
	Timeout               time.Duration
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnectionTimeout time.Duration
}

type httpHandlerConfig struct {
	Payment  PaymentConfig
	MockBank MockBankConfig
	Options  httpapi.HandlerOptions
}

func (cfg config) httpHandler() httpHandlerConfig {
	return httpHandlerConfig{
		Payment:  cfg.Payment,
		MockBank: cfg.MockBank,
		Options: httpapi.HandlerOptions{
			PaymentCommandTimeout: cfg.Payment.CommandTimeout,
			PaymentReadTimeout:    cfg.Payment.ReadTimeout,
			ReadinessTimeout:      readinessCheckTimeout,
			MaxRequestBodyBytes:   cfg.HTTP.MaxRequestBodyBytes,
		},
	}
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
		Runtime:  RuntimeConfig{LogLevel: envString("LOG_LEVEL", defaultLogLevel), ShutdownTimeout: shutdownTimeout},
		Database: DatabaseConfig{URL: os.Getenv("DATABASE_URL"), MaxOpenConnections: databaseMaxOpenConnections, MaxIdleConnections: databaseMaxIdleConnections, ConnectionMaxLifetime: databaseConnectionMaxLifetime, ConnectionMaxIdleTime: databaseConnectionMaxIdleTime, StartupTimeout: databaseStartupTimeout},
		HTTP:     HTTPConfig{Addr: envString("ADDR", defaultHTTPAddr), ReadHeaderTimeout: httpReadHeaderTimeout, ReadTimeout: httpReadTimeout, WriteTimeout: httpWriteTimeout, IdleTimeout: httpIdleTimeout, MaxRequestBodyBytes: httpMaxRequestBodyBytes},
		Payment:  PaymentConfig{FingerprintSecret: os.Getenv("FINGERPRINT_SECRET"), IdempotencyClaimStuckAfter: idempotencyClaimStuckAfter, CommandTimeout: paymentCommandTimeout, ReadTimeout: paymentReadTimeout},
		MockBank: MockBankConfig{BaseURL: os.Getenv("MOCK_BANK_BASE_URL"), Timeout: mockBankTimeout, ConnectTimeout: mockBankConnectTimeout, TLSHandshakeTimeout: mockBankTLSHandshakeTimeout, ResponseHeaderTimeout: mockBankResponseHeaderTimeout, IdleConnectionTimeout: mockBankIdleConnectionTimeout},
	}
	return cfg, nil
}

func (cfg config) validate() error {
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
	if cfg.Payment.IdempotencyClaimStuckAfter <= 0 {
		return fmt.Errorf("IDEMPOTENCY_CLAIM_STUCK_AFTER must be a positive duration")
	}
	if cfg.Runtime.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
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
	if cfg.Payment.ReadTimeout <= 0 {
		return fmt.Errorf("PAYMENT_READ_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.Timeout <= 0 {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be a positive duration")
	}
	if cfg.MockBank.Timeout >= cfg.Payment.CommandTimeout {
		return fmt.Errorf("MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT")
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
