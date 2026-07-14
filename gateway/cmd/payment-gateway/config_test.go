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
		DatabaseURL:                   validDatabaseURL,
		DatabaseMaxOpenConnections:    defaultDatabaseMaxOpenConnections,
		DatabaseMaxIdleConnections:    defaultDatabaseMaxIdleConnections,
		DatabaseConnectionMaxLifetime: defaultDatabaseConnectionMaxLifetime,
		DatabaseConnectionMaxIdleTime: defaultDatabaseConnectionMaxIdleTime,
		DatabaseStartupTimeout:        defaultDatabaseStartupTimeout,
		MockBankBaseURL:               validMockBankBaseURL,
		FingerprintSecret:             validFingerprintSecret,
		IdempotencyClaimStuckAfter:    defaultIdempotencyClaimStuckAfter,
		ShutdownTimeout:               defaultShutdownTimeout,
		LogLevel:                      "info",
		PaymentCommandTimeout:         defaultPaymentCommandTimeout,
		PaymentReadTimeout:            defaultPaymentReadTimeout,
		MockBankTimeout:               defaultMockBankTimeout,
		HTTPReadHeaderTimeout:         defaultHTTPReadHeaderTimeout,
		HTTPReadTimeout:               defaultHTTPReadTimeout,
		HTTPWriteTimeout:              defaultHTTPWriteTimeout,
		HTTPIdleTimeout:               defaultHTTPIdleTimeout,
		HTTPMaxRequestBodyBytes:       defaultHTTPMaxRequestBodyBytes,
		MockBankConnectTimeout:        defaultMockBankConnectTimeout,
		MockBankTLSHandshakeTimeout:   defaultMockBankTLSHandshakeTimeout,
		MockBankResponseHeaderTimeout: defaultMockBankResponseHeaderTimeout,
		MockBankIdleConnectionTimeout: defaultMockBankIdleConnectionTimeout,
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
	require.NoError(t, validConfig().validate())
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
		{
			name: "mock bank timeout must leave command completion time",
			mutate: func(cfg *config) {
				cfg.MockBankTimeout = cfg.PaymentCommandTimeout
			},
			wantErr: "MOCK_BANK_TIMEOUT must be shorter than PAYMENT_COMMAND_TIMEOUT",
		},
		{
			name: "HTTP write timeout must exceed command timeout",
			mutate: func(cfg *config) {
				cfg.HTTPWriteTimeout = cfg.PaymentCommandTimeout
			},
			wantErr: "HTTP_WRITE_TIMEOUT must exceed PAYMENT_COMMAND_TIMEOUT",
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
		{name: "max open connections", envName: "DATABASE_MAX_OPEN_CONNECTIONS", value: "many", wantErr: "DATABASE_MAX_OPEN_CONNECTIONS must be an integer"},
		{name: "max idle connections", envName: "DATABASE_MAX_IDLE_CONNECTIONS", value: "some", wantErr: "DATABASE_MAX_IDLE_CONNECTIONS must be an integer"},
		{name: "connection max lifetime", envName: "DATABASE_CONNECTION_MAX_LIFETIME", value: "forever", wantErr: "DATABASE_CONNECTION_MAX_LIFETIME must be a valid duration"},
		{name: "idempotency claim stuck-after", envName: "IDEMPOTENCY_CLAIM_STUCK_AFTER", value: "forever", wantErr: "IDEMPOTENCY_CLAIM_STUCK_AFTER must be a valid duration"},
		{name: "shutdown timeout", envName: "SHUTDOWN_TIMEOUT", value: "forever", wantErr: "SHUTDOWN_TIMEOUT must be a valid duration"},
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

func TestLoadConfigAllowsCustomListenAddress(t *testing.T) {
	t.Setenv("ADDR", ":9090")
	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.Equal(t, ":9090", cfg.Addr)
}
