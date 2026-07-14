package main

import (
	"testing"
	"time"

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
		Runtime:  RuntimeConfig{LogLevel: "info", ShutdownTimeout: defaultShutdownTimeout},
		Database: DatabaseConfig{URL: validDatabaseURL, MaxOpenConnections: defaultDatabaseMaxOpenConnections, MaxIdleConnections: defaultDatabaseMaxIdleConnections, ConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime, ConnectionMaxIdleTime: defaultDatabaseConnectionMaxIdleTime, StartupTimeout: defaultDatabaseStartupTimeout},
		HTTP:     HTTPConfig{Addr: ":8080", ReadHeaderTimeout: defaultHTTPReadHeaderTimeout, ReadTimeout: defaultHTTPReadTimeout, WriteTimeout: defaultHTTPWriteTimeout, IdleTimeout: defaultHTTPIdleTimeout, MaxRequestBodyBytes: defaultHTTPMaxRequestBodyBytes},
		Payment:  PaymentConfig{FingerprintSecret: validFingerprintSecret, IdempotencyClaimStuckAfter: defaultIdempotencyClaimStuckAfter, CommandTimeout: defaultPaymentCommandTimeout, ReadTimeout: defaultPaymentReadTimeout},
		MockBank: MockBankConfig{BaseURL: validMockBankBaseURL, Timeout: defaultMockBankTimeout, ConnectTimeout: defaultMockBankConnectTimeout, TLSHandshakeTimeout: defaultMockBankTLSHandshakeTimeout, ResponseHeaderTimeout: defaultMockBankResponseHeaderTimeout, IdleConnectionTimeout: defaultMockBankIdleConnectionTimeout},
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config)
		wantErr string
	}{
		{"valid", func(*config) {}, ""},
		{"database URL", func(c *config) { c.Database.URL = "" }, "DATABASE_URL is required"},
		{"pool size", func(c *config) { c.Database.MaxOpenConnections = 0 }, "DATABASE_MAX_OPEN_CONNECTIONS must be a positive integer"},
		{"pool relationship", func(c *config) { c.Database.MaxIdleConnections = c.Database.MaxOpenConnections + 1 }, "DATABASE_MAX_IDLE_CONNECTIONS must be less than or equal to DATABASE_MAX_OPEN_CONNECTIONS"},
		{"payment secret", func(c *config) { c.Payment.FingerprintSecret = "" }, "FINGERPRINT_SECRET is required"},
		{"mock bank budget", func(c *config) { c.MockBank.Timeout = c.Payment.CommandTimeout }, "MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT"},
		{"HTTP write budget", func(c *config) { c.HTTP.WriteTimeout = c.Payment.CommandTimeout }, "HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT"},
		{"log level", func(c *config) { c.Runtime.LogLevel = "verbose" }, "LOG_LEVEL must be one of debug, info, warn, or error"},
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
	t.Setenv("DATABASE_URL", validDatabaseURL)
	t.Setenv("MOCK_BANK_BASE_URL", validMockBankBaseURL)
	t.Setenv("FINGERPRINT_SECRET", validFingerprintSecret)
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.HTTP.Addr)
	assert.Equal(t, validDatabaseURL, cfg.Database.URL)
	assert.Equal(t, defaultDatabaseMaxOpenConnections, cfg.Database.MaxOpenConnections)
	assert.Equal(t, defaultDatabaseConnectionMaxIdleTime, cfg.Database.ConnectionMaxIdleTime)
	assert.Equal(t, defaultPaymentCommandTimeout, cfg.Payment.CommandTimeout)
	assert.Equal(t, defaultMockBankTimeout, cfg.MockBank.Timeout)
	assert.Equal(t, "info", cfg.Runtime.LogLevel)
	assert.Equal(t, defaultShutdownTimeout, cfg.Runtime.ShutdownTimeout)
}

func TestLoadConfigAllowsComponentConfiguration(t *testing.T) {
	t.Setenv("DATABASE_MAX_OPEN_CONNECTIONS", "20")
	t.Setenv("DATABASE_MAX_IDLE_CONNECTIONS", "8")
	t.Setenv("DATABASE_CONNECTION_MAX_LIFETIME", "45m")
	t.Setenv("PAYMENT_COMMAND_TIMEOUT", "12s")
	t.Setenv("MOCK_BANK_TIMEOUT", "8s")
	t.Setenv("HTTP_MAX_REQUEST_BODY_BYTES", "32768")
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, 20, cfg.Database.MaxOpenConnections)
	assert.Equal(t, 8, cfg.Database.MaxIdleConnections)
	assert.Equal(t, 45*time.Minute, cfg.Database.ConnectionMaxLifetime)
	assert.Equal(t, 12*time.Second, cfg.Payment.CommandTimeout)
	assert.Equal(t, 8*time.Second, cfg.MockBank.Timeout)
	assert.Equal(t, int64(32768), cfg.HTTP.MaxRequestBodyBytes)
	assert.Equal(t, "debug", cfg.Runtime.LogLevel)
}

func TestLoadConfigRejectsMalformedValues(t *testing.T) {
	for _, tt := range []struct{ name, value string }{
		{"DATABASE_MAX_OPEN_CONNECTIONS", "many"},
		{"DATABASE_CONNECTION_MAX_IDLE_TIME", "forever"},
		{"PAYMENT_COMMAND_TIMEOUT", "forever"},
		{"HTTP_MAX_REQUEST_BODY_BYTES", "large"},
		{"MOCK_BANK_CONNECT_TIMEOUT", "forever"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.name, tt.value)
			cfg, err := loadConfig()
			require.Error(t, err)
			assert.Equal(t, config{}, cfg)
		})
	}
}
